package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"runtime/debug"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/fsnotify/fsnotify"
	"github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/webp"
)

const thumbnailSize = 400

var (
	photosDir    string
	cacheDir     string
	serverPort   string
	mediumWidth  int
	appTitle     string
	videoEnabled bool
	bgColor      string
	iconsDir     string
)

var videoExts = map[string]bool{
	".mp4":  true,
	".webm": true,
	".mov":  true,
	".m4v":  true,
}

func isVideo(filename string) bool {
	return videoExts[strings.ToLower(filepath.Ext(filename))]
}

func isValidHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// extractVideoFrame runs ffmpeg to extract a single frame (5th frame, falling
// back to frame 0 for very short clips) and returns it as a decoded image.
func extractVideoFrame(srcPath string) (image.Image, error) {
	for _, filter := range []string{"select=gte(n\\,4)", "select=eq(n\\,0)"} {
		cmd := exec.Command("ffmpeg",
			"-loglevel", "error",
			"-i", srcPath,
			"-vf", filter, "-vframes", "1",
			"-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "2", "-")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			if img, _, err := image.Decode(bytes.NewReader(out)); err == nil {
				return img, nil
			}
		}
	}
	return nil, fmt.Errorf("ffmpeg: could not extract frame from %s", srcPath)
}

// frontendFS is set by embed.go / embed_dev.go before main() runs.
var frontendFS fs.FS

// rawManifest holds the built manifest.webmanifest bytes, used as a template
// for dynamic title injection.  Populated once in main() after flag parsing.
var rawManifest []byte

var supportedExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
}

// isValidFilename rejects empty names, names containing path separators, and "..".
func isValidFilename(filename string) bool {
	return filename != "" && !strings.ContainsAny(filename, "/\\") && filename != ".." && filename != "."
}

// ── Image metadata index ──────────────────────────────────────────────────
// Persisted to {cacheDir}/meta.json. Maps filename → best available date,
// derived in priority order: EXIF → filename pattern → file mtime.

type ImageMeta struct {
	Date      time.Time `json:"date"`
	FileMtime time.Time `json:"fileMtime,omitempty"` // OS mtime, used for thumb cache-busting
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
}

var (
	metaIndex  map[string]ImageMeta
	metaMu     sync.RWMutex
	metaPath   string
	metaSaveCh = make(chan struct{}, 1)
)

// filenameDate matches patterns like 20190318_132033 at the start of the base name.
var filenameDate = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})`)

func loadMetaIndex() {
	metaIndex = map[string]ImageMeta{}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &metaIndex); err != nil {
		log.Printf("meta: parse error: %v", err)
		metaIndex = map[string]ImageMeta{}
	}
}

// saveMetaIndex signals the meta saver goroutine to write meta.json.
// Non-blocking: if a save is already pending the signal is dropped (the
// pending write will use the latest state of metaIndex when it runs).
func saveMetaIndex() {
	select {
	case metaSaveCh <- struct{}{}:
	default:
	}
}

// runMetaSaver processes save signals one at a time, preventing concurrent
// writes to meta.json. Must be started after metaPath is set.
func runMetaSaver() {
	for range metaSaveCh {
		metaMu.RLock()
		data, err := json.Marshal(metaIndex)
		metaMu.RUnlock()
		if err != nil {
			log.Printf("meta: marshal error: %v", err)
			continue
		}
		tmp := metaPath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			log.Printf("meta: write error: %v", err)
			continue
		}
		if err := os.Rename(tmp, metaPath); err != nil {
			log.Printf("meta: rename error: %v", err)
		}
	}
}

func extractBestDate(filename, srcPath string) time.Time {
	// 1. EXIF DateTimeOriginal / DateTime (images only — videos have no EXIF)
	if !isVideo(filename) {
		if f, err := os.Open(srcPath); err == nil {
			if x, err := exif.Decode(f); err == nil {
				if t, err := x.DateTime(); err == nil {
					f.Close()
					return t
				}
			}
			f.Close()
		}
	}
	// 2. Filename pattern: YYYYMMDD_HHMMSS at start of base name
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if m := filenameDate.FindStringSubmatch(base); m != nil {
		s := m[1] + m[2] + m[3] + m[4] + m[5] + m[6]
		if t, err := time.ParseInLocation("20060102150405", s, time.Local); err == nil {
			return t
		}
	}
	// 3. File mtime
	if fi, err := os.Stat(srcPath); err == nil {
		return fi.ModTime()
	}
	return time.Now()
}

// indexImage extracts and stores the best date, OS mtime, and (optionally) dimensions
// for filename. Pass w=0,h=0 when dimensions are not yet known.
func indexImage(filename string, w, h int) {
	srcPath := filepath.Join(photosDir, filename)

	var fileMtime time.Time
	if fi, err := os.Stat(srcPath); err == nil {
		fileMtime = fi.ModTime()
	}

	metaMu.RLock()
	existing, exists := metaIndex[filename]
	metaMu.RUnlock()

	// Skip if fully indexed, no new dimensions, and file hasn't changed.
	if exists && existing.Width > 0 && w == 0 &&
		!fileMtime.IsZero() && existing.FileMtime.Equal(fileMtime) {
		return
	}

	// Re-extract date if file is new or its mtime changed.
	date := existing.Date
	if !exists || date.IsZero() || (!fileMtime.IsZero() && !existing.FileMtime.Equal(fileMtime)) {
		date = extractBestDate(filename, srcPath)
	}

	width, height := existing.Width, existing.Height
	if w > 0 {
		width, height = w, h
	}

	metaMu.Lock()
	metaIndex[filename] = ImageMeta{Date: date, FileMtime: fileMtime, Width: width, Height: height}
	metaMu.Unlock()

	if w > 0 {
		cacheUpdateDimensions(filename, width, height)
	}
}

// bestDate returns the indexed date for filename, or fallback if not in index.
func bestDate(filename string, fallback time.Time) time.Time {
	metaMu.RLock()
	meta, ok := metaIndex[filename]
	metaMu.RUnlock()
	if ok {
		return meta.Date
	}
	return fallback
}

// ── In-memory images cache ─────────────────────────────────────────────────
// Populated once from disk and updated incrementally on mutations.
// Avoids re-reading the photos directory on every /api/images request.

var (
	imagesCache   []ImageInfo
	imagesCacheMu sync.RWMutex
	sortedCache   map[string][]ImageInfo // keyed "by:order"; nil means invalid, cleared on any mutation
)

// buildImagesCache reads the photos directory and rebuilds imagesCache from scratch.
// Called at startup (after loadMetaIndex) so the gallery is immediately available,
// and again at the end of warmup to pick up any newly discovered metadata.
func buildImagesCache() {
	entries, err := os.ReadDir(photosDir)
	if err != nil {
		log.Printf("cache: cannot read photos dir: %v", err)
		return
	}
	list := make([]ImageInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !supportedExts[ext] {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		metaMu.RLock()
		meta := metaIndex[e.Name()]
		metaMu.RUnlock()
		fileMtime := meta.FileMtime
		if fileMtime.IsZero() {
			fileMtime = fi.ModTime()
		}
		small, medium, original := thumbURLs(e.Name())
		list = append(list, ImageInfo{
			Filename:    e.Name(),
			ModTime:     bestDate(e.Name(), fi.ModTime()),
			FileMtime:   fileMtime,
			Size:        fi.Size(),
			Width:       meta.Width,
			Height:      meta.Height,
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
		})
	}
	imagesCacheMu.Lock()
	imagesCache = list
	sortedCache = nil
	imagesCacheMu.Unlock()
}

func cacheAdd(info ImageInfo) {
	imagesCacheMu.Lock()
	for _, img := range imagesCache {
		if img.Filename == info.Filename {
			imagesCacheMu.Unlock()
			return
		}
	}
	imagesCache = append(imagesCache, info)
	sortedCache = nil
	imagesCacheMu.Unlock()
}

func cacheRemove(filename string) {
	imagesCacheMu.Lock()
	for i, img := range imagesCache {
		if img.Filename == filename {
			imagesCache = append(imagesCache[:i], imagesCache[i+1:]...)
			break
		}
	}
	sortedCache = nil
	imagesCacheMu.Unlock()
}

func cacheUpdateDimensions(filename string, w, h int) {
	small, medium, original := thumbURLs(filename)
	imagesCacheMu.Lock()
	for i, img := range imagesCache {
		if img.Filename == filename {
			imagesCache[i].Width = w
			imagesCache[i].Height = h
			imagesCache[i].ThumbSmall = small
			imagesCache[i].ThumbMedium = medium
			imagesCache[i].Original = original
			break
		}
	}
	sortedCache = nil
	imagesCacheMu.Unlock()
}

// ── Image list ────────────────────────────────────────────────────────────

type ImageInfo struct {
	Filename    string    `json:"filename"`
	ModTime     time.Time `json:"modTime"`  // best date: EXIF → filename pattern → mtime
	FileMtime   time.Time `json:"-"`        // OS mtime, used for "date modified" sort
	Size        int64     `json:"size"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
	ThumbSmall  string    `json:"thumbSmall"`
	ThumbMedium string    `json:"thumbMedium"`
	Original    string    `json:"original"`
}

type ListResponse struct {
	Images []ImageInfo `json:"images"`
	Total  int         `json:"total"`
	Page   int         `json:"page"`
	Limit  int         `json:"limit"`
}

var fileMu sync.Map

// thumbHash returns a 16-char hex string that uniquely identifies the content
// of a thumbnail: it changes whenever the source file's OS mtime changes,
// ensuring browsers fetch a fresh thumbnail after an external edit.
func thumbHash(filename string, mtime time.Time) string {
	h := sha256.Sum256([]byte(filename + "\x00" + strconv.FormatInt(mtime.UnixNano(), 10)))
	return hex.EncodeToString(h[:8])
}

func thumbSmallCachePath(filename string) string {
	return filepath.Join(cacheDir, "s", filename)
}

func thumbMediumCachePath(filename string) string {
	return filepath.Join(cacheDir, "m", filename)
}

// thumbURLs returns the API URLs for the small thumbnail, medium thumbnail, and
// original of filename. It reads FileMtime from metaIndex; if not yet recorded
// it falls back to os.Stat. The hash in each URL changes when the source file's
// mtime changes, ensuring browsers fetch fresh content after an edit.
func thumbURLs(filename string) (small, medium, original string) {
	metaMu.RLock()
	meta := metaIndex[filename]
	metaMu.RUnlock()
	mtime := meta.FileMtime
	if mtime.IsZero() {
		if fi, err := os.Stat(filepath.Join(photosDir, filename)); err == nil {
			mtime = fi.ModTime()
		}
	}
	h := thumbHash(filename, mtime)
	enc := url.PathEscape(filename)
	return "/api/thumb/" + h + "/" + enc,
		"/api/thumb-medium/" + h + "/" + enc,
		"/api/original/" + h + "/" + enc
}

// recoveryMiddleware catches panics in HTTP handlers, logs them with a stack
// trace, and returns 500. net/http already recovers handler panics but logs
// only a single line; this gives us the full trace.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// safeGo launches fn in a new goroutine. If fn panics the panic is logged with
// a stack trace and the goroutine exits (use for one-shot operations).
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic in %s: %v\n%s", name, rec, debug.Stack())
			}
		}()
		fn()
	}()
}

// safeLoop launches fn in a goroutine that restarts fn after any panic, with a
// short back-off, so long-running background loops survive unexpected errors.
func safeLoop(name string, fn func()) {
	go func() {
		for {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("panic in %s: %v\n%s", name, rec, debug.Stack())
					}
				}()
				fn()
			}()
			log.Printf("%s: restarting in 5s after unexpected exit", name)
			time.Sleep(5 * time.Second)
		}
	}()
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sortImageSlice sorts images in place. Uses SliceStable with a filename tiebreaker
// so pagination is deterministic even when multiple images share the same date.
func sortImageSlice(images []ImageInfo, by, order string) {
	switch by {
	case "name":
		if order == "desc" {
			sort.SliceStable(images, func(i, j int) bool { return images[i].Filename > images[j].Filename })
		} else {
			sort.SliceStable(images, func(i, j int) bool { return images[i].Filename < images[j].Filename })
		}
	case "mtime":
		if order == "asc" {
			sort.SliceStable(images, func(i, j int) bool {
				if images[i].FileMtime.Equal(images[j].FileMtime) {
					return images[i].Filename < images[j].Filename
				}
				return images[i].FileMtime.Before(images[j].FileMtime)
			})
		} else {
			sort.SliceStable(images, func(i, j int) bool {
				if images[i].FileMtime.Equal(images[j].FileMtime) {
					return images[i].Filename < images[j].Filename
				}
				return images[i].FileMtime.After(images[j].FileMtime)
			})
		}
	default: // "taken", "date", or unspecified → sort by EXIF/best date
		if order == "asc" {
			sort.SliceStable(images, func(i, j int) bool {
				if images[i].ModTime.Equal(images[j].ModTime) {
					return images[i].Filename < images[j].Filename
				}
				return images[i].ModTime.Before(images[j].ModTime)
			})
		} else {
			sort.SliceStable(images, func(i, j int) bool {
				if images[i].ModTime.Equal(images[j].ModTime) {
					return images[i].Filename < images[j].Filename
				}
				return images[i].ModTime.After(images[j].ModTime)
			})
		}
	}
}

func handleImages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortBy := q.Get("sort")
	order := q.Get("order")
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	paginate := q.Has("limit")
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}

	key := sortBy + ":" + order

	// Fast path: sorted view is already cached (avoids copy+sort on every request).
	imagesCacheMu.RLock()
	cached, hit := sortedCache[key]
	imagesCacheMu.RUnlock()

	if !hit {
		// Slow path: build and cache the sorted view.
		imagesCacheMu.Lock()
		if cached, hit = sortedCache[key]; !hit {
			cached = make([]ImageInfo, len(imagesCache))
			copy(cached, imagesCache)
			sortImageSlice(cached, sortBy, order)
			if sortedCache == nil {
				sortedCache = make(map[string][]ImageInfo)
			}
			sortedCache[key] = cached
		}
		imagesCacheMu.Unlock()
	}

	total := len(cached)
	var slice []ImageInfo
	if paginate {
		start := (page - 1) * limit
		end := start + limit
		if start > total {
			start = total
		}
		if end > total {
			end = total
		}
		slice = cached[start:end]
	} else {
		slice = cached
		page = 1
		limit = total
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(ListResponse{
		Images: slice,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}


func parseThumbPath(prefix, fullPath string) (hash, filename string, ok bool) {
	rest := strings.TrimPrefix(fullPath, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return
	}
	hash = rest[:slash]
	filename = rest[slash+1:] // r.URL.Path is already decoded by net/http
	if hash == "" || !isValidFilename(filename) {
		return
	}
	ok = true
	return
}

func serveImmutable(w http.ResponseWriter, r *http.Request, cachePath string) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, cachePath)
}

// serveCachedThumb is the shared implementation for small and medium thumbnail
// handlers. All sizes and crop share the same per-filename mutex so a crop
// cannot race with a concurrent thumbnail write for the same file.
func serveCachedThumb(
	w http.ResponseWriter, r *http.Request,
	prefix string,
	cachePath func(string) string,
	transform func(image.Image) image.Image,
	quality int,
) {
	_, filename, ok := parseThumbPath(prefix, r.URL.Path)
	if !ok {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	cp := cachePath(filename)

	// Cache hit: URL is content-addressed, safe to cache forever.
	if _, err := os.Stat(cp); err == nil {
		serveImmutable(w, r, cp)
		return
	}

	srcPath := filepath.Join(photosDir, filename)
	if _, err := os.Stat(srcPath); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	rawMu, _ := fileMu.LoadOrStore(filename, &sync.Mutex{})
	mu := rawMu.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	if _, err := os.Stat(cp); err == nil {
		serveImmutable(w, r, cp)
		return
	}

	var img image.Image
	var err error
	if isVideo(filename) {
		img, err = extractVideoFrame(srcPath)
	} else {
		img, err = imaging.Open(srcPath, imaging.AutoOrientation(true))
	}
	if err != nil {
		http.Error(w, "failed to decode image", http.StatusInternalServerError)
		return
	}
	b := img.Bounds()
	thumb := transform(img)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: quality}); err != nil {
		http.Error(w, "failed to encode thumbnail", http.StatusInternalServerError)
		return
	}
	data := buf.Bytes()
	if f, err := os.Create(cp); err == nil {
		if _, werr := f.Write(data); werr != nil {
			f.Close()
			os.Remove(cp)
		} else {
			f.Close()
		}
	}
	indexImage(filename, b.Dx(), b.Dy())
	saveMetaIndex()

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data) //nolint:errcheck
}

func handleThumb(w http.ResponseWriter, r *http.Request) {
	serveCachedThumb(w, r,
		"/api/thumb/", thumbSmallCachePath,
		func(img image.Image) image.Image {
			return imaging.Fit(img, thumbnailSize, thumbnailSize, imaging.Lanczos)
		},
		85,
	)
}

func handleThumbMedium(w http.ResponseWriter, r *http.Request) {
	serveCachedThumb(w, r,
		"/api/thumb-medium/", thumbMediumCachePath,
		func(img image.Image) image.Image {
			if img.Bounds().Dx() > mediumWidth {
				return imaging.Resize(img, mediumWidth, 0, imaging.Lanczos)
			}
			return img
		},
		90,
	)
}

func handleOriginal(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/original/")
	slash := strings.IndexByte(rest, '/')
	var filename, cacheControl string
	if slash < 0 {
		filename = rest
		cacheControl = "public, max-age=3600"
	} else {
		filename = rest[slash+1:]
		cacheControl = "public, max-age=31536000, immutable"
	}
	if !isValidFilename(filename) {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeFile(w, r, filepath.Join(photosDir, filename))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20) // 500 MB hard cap
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}
	if id := r.FormValue("uploadId"); id != "" {
		handleChunkedUpload(w, r, id)
		return
	}
	src, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer src.Close()
	saveUploadedFile(w, src, header.Filename)
}

func handleChunkedUpload(w http.ResponseWriter, r *http.Request, uploadID string) {
	if !validUploadID(uploadID) {
		http.Error(w, "invalid upload ID", http.StatusBadRequest)
		return
	}
	chunkIndex, err1 := strconv.Atoi(r.FormValue("chunkIndex"))
	totalChunks, err2 := strconv.Atoi(r.FormValue("totalChunks"))
	filename := filepath.Base(r.FormValue("filename"))
	if err1 != nil || err2 != nil || filename == "" || totalChunks < 1 || totalChunks > 10000 || chunkIndex < 0 || chunkIndex >= totalChunks {
		http.Error(w, "invalid chunk parameters", http.StatusBadRequest)
		return
	}

	src, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer src.Close()

	tmpDir := filepath.Join(cacheDir, "uploads", uploadID)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		http.Error(w, "failed to create upload dir", http.StatusInternalServerError)
		return
	}

	chunkPath := filepath.Join(tmpDir, fmt.Sprintf("%04d", chunkIndex))
	cf, err := os.Create(chunkPath)
	if err != nil {
		http.Error(w, "failed to create chunk", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(cf, src); err != nil {
		cf.Close()
		http.Error(w, "failed to write chunk", http.StatusInternalServerError)
		return
	}
	cf.Close()

	if chunkIndex < totalChunks-1 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "chunk_ok"})
		return
	}

	// All chunks received: assemble into a single reader and save.
	asmPath := filepath.Join(tmpDir, "assembled")
	asm, err := os.Create(asmPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		http.Error(w, "failed to assemble file", http.StatusInternalServerError)
		return
	}
	for i := 0; i < totalChunks; i++ {
		cp := filepath.Join(tmpDir, fmt.Sprintf("%04d", i))
		chunk, err := os.Open(cp)
		if err != nil {
			asm.Close()
			os.RemoveAll(tmpDir)
			http.Error(w, fmt.Sprintf("missing chunk %d", i), http.StatusInternalServerError)
			return
		}
		_, copyErr := io.Copy(asm, chunk)
		chunk.Close()
		if copyErr != nil {
			asm.Close()
			os.RemoveAll(tmpDir)
			http.Error(w, "failed to assemble file", http.StatusInternalServerError)
			return
		}
	}
	if _, err := asm.Seek(0, io.SeekStart); err != nil {
		asm.Close()
		os.RemoveAll(tmpDir)
		http.Error(w, "seek failed", http.StatusInternalServerError)
		return
	}
	saveUploadedFile(w, asm, filename)
	asm.Close()
	os.RemoveAll(tmpDir)
}

func saveUploadedFile(w http.ResponseWriter, src io.Reader, originalName string) {
	ext := strings.ToLower(filepath.Ext(originalName))
	if !supportedExts[ext] {
		http.Error(w, "unsupported file type", http.StatusUnprocessableEntity)
		return
	}
	safeName := filepath.Base(originalName)
	if safeName == "" || safeName == "." || safeName == ".." {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	destPath := filepath.Join(photosDir, safeName)

	// O_EXCL makes the existence-check and create one atomic operation.
	dst, ferr := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if ferr != nil {
		if os.IsExist(ferr) {
			http.Error(w, "file already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create file", http.StatusInternalServerError)
		return
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(destPath)
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}
	dst.Close()

	log.Printf("upload: saved %s", destPath)

	// Generate the small thumbnail synchronously so the grid can display it
	// immediately at the correct scale (requires knowing the real dimensions).
	var imgW, imgH int
	if !isVideo(safeName) {
		if srcImg, err := imaging.Open(destPath, imaging.AutoOrientation(true)); err == nil {
			b := srcImg.Bounds()
			imgW, imgH = b.Dx(), b.Dy()
			thumb := imaging.Fit(srcImg, thumbnailSize, thumbnailSize, imaging.Lanczos)
			cp := thumbSmallCachePath(safeName)
			if f, err := os.Create(cp); err == nil {
				jpeg.Encode(f, thumb, &jpeg.Options{Quality: 85}) //nolint:errcheck
				f.Close()
			}
		}
	}

	indexImage(safeName, imgW, imgH)
	saveMetaIndex()

	small, medium, original := thumbURLs(safeName)
	if fi, err := os.Stat(destPath); err == nil {
		cacheAdd(ImageInfo{
			Filename:    safeName,
			ModTime:     bestDate(safeName, fi.ModTime()),
			FileMtime:   fi.ModTime(),
			Size:        fi.Size(),
			Width:       imgW,
			Height:      imgH,
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"filename":    safeName,
		"thumbSmall":  small,
		"thumbMedium": medium,
		"original":    original,
	})
}

func validUploadID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func sweepOrphanedUploads() {
	dir := filepath.Join(cacheDir, "uploads")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

func handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filename := strings.TrimPrefix(r.URL.Path, "/api/delete/")
	if !isValidFilename(filename) {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	srcPath := filepath.Join(photosDir, filename)

	if err := os.Remove(srcPath); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to delete", http.StatusInternalServerError)
		}
		return
	}
	os.Remove(thumbSmallCachePath(filename))
	os.Remove(thumbMediumCachePath(filename))
	fileMu.Delete(filename)
	metaMu.Lock()
	delete(metaIndex, filename)
	metaMu.Unlock()
	cacheRemove(filename)
	saveMetaIndex()
	log.Printf("delete: %s", srcPath)
	w.WriteHeader(http.StatusNoContent)
}

// extractJPEGApp1 returns the raw APP1 (EXIF) marker segment from a JPEG,
// or nil if the file has no EXIF APP1.
func extractJPEGApp1(data []byte) []byte {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil
	}
	i := 2
	for i+3 < len(data) {
		if data[i] != 0xFF {
			return nil
		}
		marker := data[i+1]
		if marker == 0xD9 || marker == 0xDA {
			return nil
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		end := i + 2 + segLen
		if end > len(data) {
			return nil
		}
		if marker == 0xE1 && segLen >= 8 && string(data[i+4:i+10]) == "Exif\x00\x00" {
			return data[i:end]
		}
		i = end
	}
	return nil
}

// resetExifOrientation returns a copy of an APP1 segment with two changes:
//  1. The IFD0 orientation tag is set to 1 (TopLeft / no rotation), because
//     imaging.AutoOrientation has already baked the rotation into the pixels.
//  2. The IFD1 next-pointer is zeroed, dropping the stale embedded JPEG
//     thumbnail. Without this, file managers extract the pre-crop, pre-rotate
//     thumbnail and display it in the wrong orientation.
func resetExifOrientation(app1 []byte) []byte {
	if len(app1) < 18 {
		return app1
	}
	// TIFF data begins at byte 10: FF E1 (2) + length (2) + "Exif\0\0" (6)
	tiff := app1[10:]
	if len(tiff) < 8 {
		return app1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return app1
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return app1
	}
	ifd0 := int(bo.Uint32(tiff[4:8]))
	if ifd0+2 > len(tiff) {
		return app1
	}
	n := int(bo.Uint16(tiff[ifd0:]))

	out := make([]byte, len(app1))
	copy(out, app1)
	tiffOut := out[10:]

	// 1. Reset orientation tag to TopLeft.
	for i := range n {
		off := ifd0 + 2 + i*12
		if off+12 > len(tiff) {
			break
		}
		if bo.Uint16(tiff[off:]) == 0x0112 {
			bo.PutUint16(tiffOut[off+8:], 1)
			break
		}
	}

	// 2. Zero the IFD1 next-pointer (4 bytes immediately after IFD0 entries).
	nextPtr := ifd0 + 2 + n*12
	if nextPtr+4 <= len(tiff) {
		bo.PutUint32(tiffOut[nextPtr:], 0)
	}

	return out
}

// injectJPEGApp1 inserts an APP1 segment immediately after the SOI marker of
// a JPEG byte slice and returns the result.
func injectJPEGApp1(dst, app1 []byte) []byte {
	if len(dst) < 2 {
		return dst
	}
	out := make([]byte, 0, len(dst)+len(app1))
	out = append(out, dst[:2]...)  // SOI
	out = append(out, app1...)     // EXIF APP1
	out = append(out, dst[2:]...)  // remainder
	return out
}

func handleCrop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filename := strings.TrimPrefix(r.URL.Path, "/api/crop/")
	if !isValidFilename(filename) {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	var body struct {
		X      int `json:"x"`
		Y      int `json:"y"`
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Width <= 0 || body.Height <= 0 {
		http.Error(w, "width and height must be positive", http.StatusBadRequest)
		return
	}

	srcPath := filepath.Join(photosDir, filename)

	// Serialize all operations on this file through its per-file mutex.
	rawMu, _ := fileMu.LoadOrStore(filename, &sync.Mutex{})
	mu := rawMu.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Read raw bytes first so we can copy EXIF into the re-encoded file.
	originalData, err := os.ReadFile(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "image not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to read image", http.StatusInternalServerError)
		}
		return
	}

	img, err := imaging.Decode(bytes.NewReader(originalData), imaging.AutoOrientation(true))
	if err != nil {
		http.Error(w, "failed to decode image", http.StatusInternalServerError)
		return
	}

	bounds := img.Bounds()
	x0 := body.X
	y0 := body.Y
	x1 := body.X + body.Width
	y1 := body.Y + body.Height

	// Clamp to actual image dimensions.
	if x0 < 0 { x0 = 0 }
	if y0 < 0 { y0 = 0 }
	if x1 > bounds.Dx() { x1 = bounds.Dx() }
	if y1 > bounds.Dy() { y1 = bounds.Dy() }
	if x1-x0 <= 0 || y1-y0 <= 0 {
		http.Error(w, "crop rectangle is outside image bounds", http.StatusBadRequest)
		return
	}

	cropped := imaging.Crop(img, image.Rect(x0, y0, x1, y1))
	b := cropped.Bounds()

	// Encode to a buffer, then atomically replace the source file.
	// Using srcPath+".tmp" keeps the temp file on the same filesystem so
	// os.Rename is atomic; the ".tmp" extension is not in supportedExts so
	// the file watcher ignores it.
	format, err := imaging.FormatFromFilename(srcPath)
	if err != nil {
		format = imaging.JPEG
	}
	var buf bytes.Buffer
	if err := imaging.Encode(&buf, cropped, format); err != nil {
		http.Error(w, "failed to encode cropped image", http.StatusInternalServerError)
		return
	}
	outData := buf.Bytes()
	if format == imaging.JPEG {
		if app1 := extractJPEGApp1(originalData); app1 != nil {
			outData = injectJPEGApp1(outData, resetExifOrientation(app1))
		}
	}
	tmpPath := srcPath + ".tmp"
	if err := os.WriteFile(tmpPath, outData, 0o644); err != nil {
		http.Error(w, "failed to write temp file", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmpPath, srcPath); err != nil {
		os.Remove(tmpPath)
		http.Error(w, "failed to replace image", http.StatusInternalServerError)
		return
	}

	// Evict stale thumbnail caches and update metadata with new dimensions.
	os.Remove(thumbSmallCachePath(filename))
	os.Remove(thumbMediumCachePath(filename))
	indexImage(filename, b.Dx(), b.Dy())
	saveMetaIndex()

	fi, statErr := os.Stat(srcPath)
	small, medium, original := thumbURLs(filename)

	imagesCacheMu.Lock()
	for i, entry := range imagesCache {
		if entry.Filename == filename {
			if statErr == nil {
				imagesCache[i].Size = fi.Size()
				imagesCache[i].FileMtime = fi.ModTime()
				imagesCache[i].ModTime = bestDate(filename, fi.ModTime())
			}
			imagesCache[i].Width = b.Dx()
			imagesCache[i].Height = b.Dy()
			imagesCache[i].ThumbSmall = small
			imagesCache[i].ThumbMedium = medium
			imagesCache[i].Original = original
			sortedCache = nil
			break
		}
	}
	imagesCacheMu.Unlock()

	log.Printf("crop: %s (%dx%d)", filename, b.Dx(), b.Dy())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"filename":    filename,
		"width":       b.Dx(),
		"height":      b.Dy(),
		"thumbSmall":  small,
		"thumbMedium": medium,
		"original":    original,
	})
}

func warmupThumbnails() {
	log.Println("warmup: cache pre-warming started")
	defer log.Println("warmup: all thumbnails ready")
	entries, err := os.ReadDir(photosDir)
	if err != nil {
		return
	}

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !supportedExts[ext] {
			continue
		}
		name := e.Name()

		fi, err := e.Info()
		if err != nil {
			continue
		}
		fileMtime := fi.ModTime()

		metaMu.RLock()
		meta := metaIndex[name]
		metaMu.RUnlock()

		mtimeChanged := !meta.FileMtime.Equal(fileMtime)
		hasDims := meta.Width > 0

		needsSmall := func() bool {
			_, err := os.Stat(thumbSmallCachePath(name))
			return err != nil
		}()
		needsMedium := func() bool {
			_, err := os.Stat(thumbMediumCachePath(name))
			return err != nil
		}()

		if hasDims && !needsSmall && !needsMedium && !mtimeChanged {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(filename string, genSmall, genMedium bool) {
			defer wg.Done()
			defer func() { <-sem }()

			srcPath := filepath.Join(photosDir, filename)
			var img image.Image
			var err error
			if isVideo(filename) {
				img, err = extractVideoFrame(srcPath)
			} else {
				img, err = imaging.Open(srcPath, imaging.AutoOrientation(true))
			}
			if err != nil {
				log.Printf("warmup: open %s: %v", filename, err)
				indexImage(filename, 0, 0)
				return
			}
			b := img.Bounds()
			indexImage(filename, b.Dx(), b.Dy()) // updates FileMtime in meta
			if genSmall {
				thumb := imaging.Fit(img, thumbnailSize, thumbnailSize, imaging.Lanczos)
				if f, err := os.Create(thumbSmallCachePath(filename)); err == nil {
					jpeg.Encode(f, thumb, &jpeg.Options{Quality: 85}) //nolint:errcheck
					f.Close()
				}
			}
			if genMedium {
				var thumb image.Image
				if b.Dx() > mediumWidth {
					thumb = imaging.Resize(img, mediumWidth, 0, imaging.Lanczos)
				} else {
					thumb = img
				}
				if f, err := os.Create(thumbMediumCachePath(filename)); err == nil {
					jpeg.Encode(f, thumb, &jpeg.Options{Quality: 90}) //nolint:errcheck
					f.Close()
				}
			}
		}(name, needsSmall || mtimeChanged, needsMedium || mtimeChanged)
	}
	wg.Wait()
	saveMetaIndex()
	// Rebuild cache after warmup to pick up newly discovered dimensions and URLs.
	buildImagesCache()
}

// updateCachedFile refreshes in-memory state for a file that was created or
// modified externally: evicts stale thumbnails, re-indexes, and updates
// imagesCache. When addIfMissing is true the file is also added to the cache
// if it was not already present (used for Create events).
func updateCachedFile(filename string, fi os.FileInfo, addIfMissing bool) {
	os.Remove(thumbSmallCachePath(filename))
	os.Remove(thumbMediumCachePath(filename))
	indexImage(filename, 0, 0)
	saveMetaIndex()
	small, medium, original := thumbURLs(filename)
	metaMu.RLock()
	meta := metaIndex[filename]
	metaMu.RUnlock()

	imagesCacheMu.Lock()
	found := false
	for i, img := range imagesCache {
		if img.Filename == filename {
			imagesCache[i].ModTime = bestDate(filename, fi.ModTime())
			imagesCache[i].FileMtime = fi.ModTime()
			imagesCache[i].Size = fi.Size()
			imagesCache[i].Width = meta.Width
			imagesCache[i].Height = meta.Height
			imagesCache[i].ThumbSmall = small
			imagesCache[i].ThumbMedium = medium
			imagesCache[i].Original = original
			sortedCache = nil
			found = true
			break
		}
	}
	imagesCacheMu.Unlock()

	if addIfMissing && !found {
		cacheAdd(ImageInfo{
			Filename:    filename,
			ModTime:     bestDate(filename, fi.ModTime()),
			FileMtime:   fi.ModTime(),
			Size:        fi.Size(),
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
		})
		log.Printf("watcher: added %s", filename)
	} else {
		log.Printf("watcher: updated %s", filename)
	}
}

// watchPhotosDir watches photosDir with fsnotify and keeps imagesCache and
// metaIndex in sync when files are added, modified, or removed externally.
func watchPhotosDir() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watcher: failed to create: %v", err)
		return
	}
	if err := watcher.Add(photosDir); err != nil {
		log.Printf("watcher: failed to watch %s: %v", photosDir, err)
		watcher.Close()
		return
	}
	log.Printf("watcher: watching %s", photosDir)

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							log.Printf("panic in watcher event handler: %v\n%s", rec, debug.Stack())
						}
					}()
					filename := filepath.Base(event.Name)
					ext := strings.ToLower(filepath.Ext(filename))
					if !supportedExts[ext] {
						return
					}

					switch {
					case event.Has(fsnotify.Create):
						// Wait briefly for the file to be fully written.
						time.Sleep(200 * time.Millisecond)
						fi, err := os.Stat(event.Name)
						if err != nil {
							return
						}
						// Do NOT delete metaIndex entry: indexImage preserves existing
						// Width/Height when called with w=0, and detects mtime changes.
						updateCachedFile(filename, fi, true)

					case event.Has(fsnotify.Write):
						fi, err := os.Stat(event.Name)
						if err != nil {
							return
						}
						updateCachedFile(filename, fi, false)

					case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
						os.Remove(thumbSmallCachePath(filename))
						os.Remove(thumbMediumCachePath(filename))
						fileMu.Delete(filename)
						metaMu.Lock()
						delete(metaIndex, filename)
						metaMu.Unlock()
						cacheRemove(filename)
						saveMetaIndex()
						log.Printf("watcher: removed %s", filename)
					}
				}()

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("watcher: error: %v", err)
			}
		}
	}()
}


// reconcile cross-checks the photos directory, imagesCache, and metaIndex and
// brings them into agreement. It removes stale in-memory entries and thumbnail
// files for photos deleted from disk, and adds cache entries for any photos
// that are on disk but missing from the in-memory index.
func reconcile() {
	log.Println("reconcile: starting nightly consistency check")

	entries, err := os.ReadDir(photosDir)
	if err != nil {
		log.Printf("reconcile: cannot read photos dir: %v", err)
		return
	}

	onDisk := make(map[string]os.FileInfo, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !supportedExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		if fi, err := e.Info(); err == nil {
			onDisk[e.Name()] = fi
		}
	}

	changed := false

	// Collect cache entries whose files are gone; also build the inCache set.
	imagesCacheMu.RLock()
	var stale []string
	inCache := make(map[string]bool, len(imagesCache))
	for _, img := range imagesCache {
		if _, ok := onDisk[img.Filename]; ok {
			inCache[img.Filename] = true
		} else {
			stale = append(stale, img.Filename)
		}
	}
	imagesCacheMu.RUnlock()

	// Evict stale entries without holding imagesCacheMu across metaMu.
	for _, name := range stale {
		os.Remove(thumbSmallCachePath(name))
		os.Remove(thumbMediumCachePath(name))
		fileMu.Delete(name)
		cacheRemove(name)
		metaMu.Lock()
		delete(metaIndex, name)
		metaMu.Unlock()
		log.Printf("reconcile: evicted stale entry %s", name)
		changed = true
	}

	// Add files on disk that are missing from the cache.
	for name, fi := range onDisk {
		if inCache[name] {
			continue
		}
		indexImage(name, 0, 0)
		small, medium, original := thumbURLs(name)
		cacheAdd(ImageInfo{
			Filename:    name,
			ModTime:     bestDate(name, fi.ModTime()),
			FileMtime:   fi.ModTime(),
			Size:        fi.Size(),
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
		})
		log.Printf("reconcile: added missing file %s", name)
		changed = true
	}

	// Prune orphaned metaIndex entries (no corresponding file on disk).
	// Collect under a read lock, then delete one at a time so readers are
	// not blocked for the full scan.
	metaMu.RLock()
	var orphaned []string
	for name := range metaIndex {
		if _, ok := onDisk[name]; !ok {
			orphaned = append(orphaned, name)
		}
	}
	metaMu.RUnlock()
	for _, name := range orphaned {
		metaMu.Lock()
		delete(metaIndex, name)
		metaMu.Unlock()
		changed = true
	}

	if changed {
		saveMetaIndex()
		log.Println("reconcile: completed with changes")
	} else {
		log.Println("reconcile: completed, everything aligned")
	}
}

// scheduleNightlyReconcile runs reconcile every day at 03:00 local time.
func scheduleNightlyReconcile() {
	go func() {
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
			if !next.After(now) {
				next = next.Add(24 * time.Hour)
			}
			time.Sleep(time.Until(next))
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("panic in reconcile: %v\n%s", rec, debug.Stack())
					}
				}()
				reconcile()
			}()
		}
	}()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// handleConfig returns runtime configuration consumed by the Vue frontend.
func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"title":        appTitle,
		"videoEnabled": videoEnabled,
		"bgColor":      bgColor,
	})
}

// handleManifest serves the PWA manifest with name/short_name replaced by the
// configured title so two parallel instances appear as distinct installed apps.
func handleManifest(w http.ResponseWriter, r *http.Request) {
	if rawManifest == nil {
		http.Error(w, "manifest not available", http.StatusInternalServerError)
		return
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		http.Error(w, "manifest parse error", http.StatusInternalServerError)
		return
	}
	m["name"] = appTitle
	m["short_name"] = appTitle
	m["background_color"] = bgColor
	if videoEnabled {
		if st, ok := m["share_target"].(map[string]any); ok {
			if params, ok := st["params"].(map[string]any); ok {
				if files, ok := params["files"].([]any); ok && len(files) > 0 {
					if f0, ok := files[0].(map[string]any); ok {
						if accept, ok := f0["accept"].([]any); ok {
							f0["accept"] = append(accept, "video/mp4", "video/webm", "video/quicktime")
						}
					}
				}
			}
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		http.Error(w, "manifest encode error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data) //nolint:errcheck
}

// handleIcons serves icon files from iconsDir when set, falling back to the
// embedded frontend assets. Allows custom icons without rebuilding the binary.
func handleIcons(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/icons/")
	if !isValidFilename(name) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}

	if iconsDir != "" {
		p := filepath.Join(iconsDir, name)
		if _, err := os.Stat(p); err == nil {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			http.ServeFile(w, r, p)
			return
		}
	}

	if frontendFS != nil {
		data, err := fs.ReadFile(frontendFS, "icons/"+name)
		if err == nil {
			mt := mime.TypeByExtension(filepath.Ext(name))
			if mt == "" {
				mt = "application/octet-stream"
			}
			w.Header().Set("Content-Type", mt)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Write(data) //nolint:errcheck
			return
		}
	}

	http.NotFound(w, r)
}

func main() {
	flag.StringVar(&photosDir, "photos", envOr("PHOTOS_DIR", "./photos"), "directory containing source images (env: PHOTOS_DIR)")
	flag.StringVar(&cacheDir, "cache", envOr("CACHE_DIR", "./cache"), "directory for thumbnail cache (env: CACHE_DIR)")
	flag.StringVar(&serverPort, "port", envOr("PORT", "8080"), "port to listen on (env: PORT)")
	flag.StringVar(&appTitle, "title", envOr("APP_TITLE", "Photo Frame"), "app title shown in the browser tab and header (env: APP_TITLE)")
	mediumWidthDefault := 2000
	if v, err := strconv.Atoi(envOr("MEDIUM_WIDTH", "")); err == nil && v > 0 {
		mediumWidthDefault = v
	}
	flag.IntVar(&mediumWidth, "medium-width", mediumWidthDefault, "max pixel width for medium thumbnails (env: MEDIUM_WIDTH)")
	videoDefault := strings.EqualFold(envOr("VIDEO", ""), "1") || strings.EqualFold(envOr("VIDEO", ""), "true")
	flag.BoolVar(&videoEnabled, "video", videoDefault, "enable mp4 video upload and thumbnails; requires ffmpeg (env: VIDEO=1)")
	flag.StringVar(&bgColor, "bg-color", envOr("BG_COLOR", "#0a0a0f"), "primary background hex colour (env: BG_COLOR)")
	flag.StringVar(&iconsDir, "icons-dir", envOr("ICONS_DIR", ""), "directory with custom icon files; falls back to embedded (env: ICONS_DIR)")
	flag.Parse()

	if !isValidHexColor(bgColor) {
		log.Printf("warning: invalid BG_COLOR %q, falling back to #0a0a0f", bgColor)
		bgColor = "#0a0a0f"
	}

	if videoEnabled {
		for ext, mt := range map[string]string{
			".mp4":  "video/mp4",
			".webm": "video/webm",
			".mov":  "video/quicktime",
			".m4v":  "video/mp4",
		} {
			supportedExts[ext] = true
			mime.AddExtensionType(ext, mt)
		}
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			log.Println("warning: ffmpeg not found in PATH — video thumbnails will fail")
		}
	}

	for _, p := range []*string{&photosDir, &cacheDir} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			log.Fatalf("cannot resolve path %s: %v", *p, err)
		}
		*p = abs
	}

	for _, dir := range []string{cacheDir, photosDir, filepath.Join(cacheDir, "s"), filepath.Join(cacheDir, "m")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("cannot create directory %s: %v", dir, err)
		}
	}

	log.Printf("photos : %s", photosDir)
	log.Printf("cache  : %s", cacheDir)
	log.Printf("title  : %s", appTitle)

	// Read the built manifest once so handleManifest can inject the title.
	if frontendFS != nil {
		if data, err := fs.ReadFile(frontendFS, "manifest.webmanifest"); err == nil {
			rawManifest = data
		} else {
			log.Printf("manifest: could not read: %v", err)
		}
	}

	metaPath = filepath.Join(cacheDir, "meta.json")
	loadMetaIndex()
	safeLoop("meta-saver", runMetaSaver)

	// Build the initial image cache synchronously so the gallery is available
	// immediately; warmup will update dimensions incrementally as it runs.
	buildImagesCache()

	safeGo("warmup", warmupThumbnails)
	go sweepOrphanedUploads()
	watchPhotosDir()
	scheduleNightlyReconcile()

	log.Printf("medium-width: %dpx", mediumWidth)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/images", handleImages)
	mux.HandleFunc("/api/config", handleConfig)
	mux.HandleFunc("/api/thumb/", handleThumb)
	mux.HandleFunc("/api/thumb-medium/", handleThumbMedium)
	mux.HandleFunc("/api/original/", handleOriginal)
	mux.HandleFunc("/api/upload", handleUpload)
	mux.HandleFunc("/api/crop/", handleCrop)
	mux.HandleFunc("/api/delete/", handleDelete)
	mux.HandleFunc("/manifest.webmanifest", handleManifest)
	mux.HandleFunc("/icons/", handleIcons)
	mux.Handle("/", frontendHandler())

	addr := serverPort
	if !strings.HasPrefix(addr, ":") {
		addr = ":" + addr
	}
	log.Printf("listening on %s", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      corsMiddleware(recoveryMiddleware(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

// frontendHandler is provided by either embed.go (production) or embed_dev.go (dev build tag).
// It returns an http.Handler that serves the compiled Vue frontend.
var frontendHandler func() http.Handler

// spaHandler wraps a filesystem handler so that any path not found in the FS
// is served as index.html — required for client-side routing.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")

		// index.html and sw.js keep the same filename across builds, so the
		// browser must revalidate them on every load rather than serving stale
		// cached copies that reference old hashed assets.
		if p == "" || p == "index.html" || p == "sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}

		// Try to open the requested path in the FS
		f, err := fsys.Open(p)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for client-side routing
		w.Header().Set("Cache-Control", "no-cache")
		r2 := *r
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, &r2)
	})
}
