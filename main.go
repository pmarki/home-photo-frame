package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
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
	photosDir   string
	cacheDir    string
	serverPort  string
	mediumWidth int
	appTitle    string
)

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
	return filename != "" && !strings.ContainsAny(filename, "/\\") && filename != ".."
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
	// 1. EXIF DateTimeOriginal / DateTime
	if f, err := os.Open(srcPath); err == nil {
		if x, err := exif.Decode(f); err == nil {
			if t, err := x.DateTime(); err == nil {
				f.Close()
				return t
			}
		}
		f.Close()
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
		small, medium := thumbURLs(e.Name())
		list = append(list, ImageInfo{
			Filename:    e.Name(),
			ModTime:     bestDate(e.Name(), fi.ModTime()),
			FileMtime:   fileMtime,
			Size:        fi.Size(),
			Width:       meta.Width,
			Height:      meta.Height,
			ThumbSmall:  small,
			ThumbMedium: medium,
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
	small, medium := thumbURLs(filename)
	imagesCacheMu.Lock()
	for i, img := range imagesCache {
		if img.Filename == filename {
			imagesCache[i].Width = w
			imagesCache[i].Height = h
			imagesCache[i].ThumbSmall = small
			imagesCache[i].ThumbMedium = medium
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

func thumbSmallCachePath(hash string) string {
	return filepath.Join(cacheDir, hash+".jpg")
}

func thumbMediumCachePath(hash string) string {
	return filepath.Join(cacheDir, hash+"-medium.jpg")
}

// thumbURLs returns the API URLs for the small and medium thumbnails of filename.
// It reads FileMtime from metaIndex; if not yet recorded it falls back to os.Stat.
func thumbURLs(filename string) (small, medium string) {
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
	return "/api/thumb/" + h + "/" + enc, "/api/thumb-medium/" + h + "/" + enc
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
// handlers. muPrefix distinguishes per-file mutex keys between the two sizes.
func serveCachedThumb(
	w http.ResponseWriter, r *http.Request,
	prefix string,
	cachePath func(string) string,
	muPrefix string,
	transform func(image.Image) image.Image,
	quality int,
) {
	hash, filename, ok := parseThumbPath(prefix, r.URL.Path)
	if !ok {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	cp := cachePath(hash)

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

	rawMu, _ := fileMu.LoadOrStore(muPrefix+filename, &sync.Mutex{})
	mu := rawMu.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	if _, err := os.Stat(cp); err == nil {
		serveImmutable(w, r, cp)
		return
	}

	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
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
		f.Write(data) //nolint:errcheck
		f.Close()
	}
	indexImage(filename, b.Dx(), b.Dy())
	saveMetaIndex()

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data) //nolint:errcheck
}

func handleThumb(w http.ResponseWriter, r *http.Request) {
	serveCachedThumb(w, r,
		"/api/thumb/", thumbSmallCachePath, "",
		func(img image.Image) image.Image {
			return imaging.Fit(img, thumbnailSize, thumbnailSize, imaging.Lanczos)
		},
		85,
	)
}

func handleThumbMedium(w http.ResponseWriter, r *http.Request) {
	serveCachedThumb(w, r,
		"/api/thumb-medium/", thumbMediumCachePath, "medium:",
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
	filename := strings.TrimPrefix(r.URL.Path, "/api/original/")
	if !isValidFilename(filename) {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, filepath.Join(photosDir, filename))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	src, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer src.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !supportedExts[ext] {
		http.Error(w, "unsupported file type", http.StatusUnprocessableEntity)
		return
	}

	safeName := filepath.Base(header.Filename)
	if safeName == "" || safeName == "." || safeName == ".." {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	destPath := filepath.Join(photosDir, safeName)

	// Use O_EXCL so the existence-check and create are one atomic operation,
	// avoiding the TOCTOU race of Stat-then-Create.
	dst, ferr := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if ferr != nil {
		if os.IsExist(ferr) {
			http.Error(w, "file already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	log.Printf("upload: saved %s", destPath)
	indexImage(safeName, 0, 0)
	saveMetaIndex()

	small, medium := thumbURLs(safeName)
	if fi, err := os.Stat(destPath); err == nil {
		cacheAdd(ImageInfo{
			Filename:    safeName,
			ModTime:     bestDate(safeName, fi.ModTime()),
			FileMtime:   fi.ModTime(),
			Size:        fi.Size(),
			ThumbSmall:  small,
			ThumbMedium: medium,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"filename":    safeName,
		"thumbSmall":  small,
		"thumbMedium": medium,
	})
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
	// Capture thumb hash before removing from meta.
	metaMu.RLock()
	thumbH := thumbHash(filename, metaIndex[filename].FileMtime)
	metaMu.RUnlock()

	if err := os.Remove(srcPath); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to delete", http.StatusInternalServerError)
		}
		return
	}
	os.Remove(thumbSmallCachePath(thumbH))
	os.Remove(thumbMediumCachePath(thumbH))
	fileMu.Delete(filename)
	fileMu.Delete("medium:" + filename)
	metaMu.Lock()
	delete(metaIndex, filename)
	metaMu.Unlock()
	cacheRemove(filename)
	saveMetaIndex()
	log.Printf("delete: %s", srcPath)
	w.WriteHeader(http.StatusNoContent)
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

	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "image not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to decode image", http.StatusInternalServerError)
		}
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

	// Save as a new file: {base}_crop_{timestamp}{ext}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	newName := fmt.Sprintf("%s_crop_%d%s", base, time.Now().UnixMilli(), ext)
	destPath := filepath.Join(photosDir, newName)

	if err := imaging.Save(cropped, destPath); err != nil {
		http.Error(w, "failed to save cropped image", http.StatusInternalServerError)
		return
	}

	b := cropped.Bounds()
	indexImage(newName, b.Dx(), b.Dy())

	// Delete the original and its thumbnails.
	metaMu.RLock()
	origThumbH := thumbHash(filename, metaIndex[filename].FileMtime)
	metaMu.RUnlock()
	os.Remove(srcPath)
	os.Remove(thumbSmallCachePath(origThumbH))
	os.Remove(thumbMediumCachePath(origThumbH))
	metaMu.Lock()
	delete(metaIndex, filename)
	metaMu.Unlock()
	cacheRemove(filename)

	saveMetaIndex()

	var size int64
	var cropMtime time.Time
	if fi, err := os.Stat(destPath); err == nil {
		size = fi.Size()
		cropMtime = fi.ModTime()
	}
	small, medium := thumbURLs(newName)
	cacheAdd(ImageInfo{
		Filename:    newName,
		ModTime:     bestDate(newName, time.Now()),
		FileMtime:   cropMtime,
		Size:        size,
		Width:       b.Dx(),
		Height:      b.Dy(),
		ThumbSmall:  small,
		ThumbMedium: medium,
	})

	log.Printf("crop: %s → %s (%dx%d), original deleted", filename, newName, b.Dx(), b.Dy())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"filename":    newName,
		"width":       b.Dx(),
		"height":      b.Dy(),
		"thumbSmall":  small,
		"thumbMedium": medium,
	})
}

func warmupThumbnails() {
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

		// Compute the expected hash-based thumb path.
		h := thumbHash(name, fileMtime)
		cp := thumbSmallCachePath(h)
		needsThumb := func() bool {
			_, err := os.Stat(cp)
			return err != nil
		}()

		if hasDims && !needsThumb && !mtimeChanged {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(filename string, genThumb bool) {
			defer wg.Done()
			defer func() { <-sem }()

			srcPath := filepath.Join(photosDir, filename)
			img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
			if err != nil {
				log.Printf("warmup: open %s: %v", filename, err)
				indexImage(filename, 0, 0)
				return
			}
			b := img.Bounds()
			indexImage(filename, b.Dx(), b.Dy()) // updates FileMtime in meta
			if !genThumb {
				return
			}
			// Recompute hash using the now-updated FileMtime in meta.
			metaMu.RLock()
			updatedMtime := metaIndex[filename].FileMtime
			metaMu.RUnlock()
			thumbPath := thumbSmallCachePath(thumbHash(filename, updatedMtime))
			thumb := imaging.Fit(img, thumbnailSize, thumbnailSize, imaging.Lanczos)
			if f, err := os.Create(thumbPath); err == nil {
				jpeg.Encode(f, thumb, &jpeg.Options{Quality: 85}) //nolint:errcheck
				f.Close()
			}
		}(name, needsThumb || mtimeChanged)
	}
	wg.Wait()
	saveMetaIndex()
	// Rebuild cache after warmup to pick up newly discovered dimensions and URLs.
	buildImagesCache()
	log.Println("warmup: all thumbnails ready")
}

// updateCachedFile refreshes in-memory state for a file that was created or
// modified externally: evicts stale thumbnails, re-indexes, and updates
// imagesCache. When addIfMissing is true the file is also added to the cache
// if it was not already present (used for Create events).
func updateCachedFile(filename string, fi os.FileInfo, addIfMissing bool) {
	metaMu.RLock()
	oldH := thumbHash(filename, metaIndex[filename].FileMtime)
	metaMu.RUnlock()
	os.Remove(thumbSmallCachePath(oldH))
	os.Remove(thumbMediumCachePath(oldH))
	indexImage(filename, 0, 0)
	saveMetaIndex()
	small, medium := thumbURLs(filename)
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
						metaMu.RLock()
						oldH := thumbHash(filename, metaIndex[filename].FileMtime)
						metaMu.RUnlock()
						os.Remove(thumbSmallCachePath(oldH))
						os.Remove(thumbMediumCachePath(oldH))
						fileMu.Delete(filename)
						fileMu.Delete("medium:" + filename)
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
		metaMu.RLock()
		h := thumbHash(name, metaIndex[name].FileMtime)
		metaMu.RUnlock()
		os.Remove(thumbSmallCachePath(h))
		os.Remove(thumbMediumCachePath(h))
		fileMu.Delete(name)
		fileMu.Delete("medium:" + name)
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
		small, medium := thumbURLs(name)
		cacheAdd(ImageInfo{
			Filename:    name,
			ModTime:     bestDate(name, fi.ModTime()),
			FileMtime:   fi.ModTime(),
			Size:        fi.Size(),
			ThumbSmall:  small,
			ThumbMedium: medium,
		})
		log.Printf("reconcile: added missing file %s", name)
		changed = true
	}

	// Prune orphaned metaIndex entries (no corresponding file on disk).
	metaMu.Lock()
	for name := range metaIndex {
		if _, ok := onDisk[name]; !ok {
			delete(metaIndex, name)
			changed = true
		}
	}
	metaMu.Unlock()

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
	json.NewEncoder(w).Encode(map[string]string{"title": appTitle})
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
	data, err := json.Marshal(m)
	if err != nil {
		http.Error(w, "manifest encode error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data) //nolint:errcheck
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
	flag.Parse()

	for _, p := range []*string{&photosDir, &cacheDir} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			log.Fatalf("cannot resolve path %s: %v", *p, err)
		}
		*p = abs
	}

	for _, dir := range []string{cacheDir, photosDir} {
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
	mux.Handle("/", frontendHandler())

	addr := serverPort
	if !strings.HasPrefix(addr, ":") {
		addr = ":" + addr
	}
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, corsMiddleware(recoveryMiddleware(mux))))
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
