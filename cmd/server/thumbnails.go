package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"
)

const thumbnailSize = 400

// fileLocks serializes work on the same file (crop, upload, thumbnail
// generation, delete, move/copy). Paths hash onto a fixed set of stripes, so
// memory stays bounded and a lock can never be dropped while held — unlike a
// per-path map, where deleting an entry another goroutine still holds lets
// the next caller create a second mutex for the same path. Unrelated paths
// occasionally share a stripe; that only costs a brief wait.
var fileLocks [256]sync.Mutex

func fileStripe(imgPath string) int {
	h := fnv.New32a()
	h.Write([]byte(imgPath)) //nolint:errcheck
	return int(h.Sum32() % uint32(len(fileLocks)))
}

// lockFile acquires the lock for imgPath and returns it locked. Never call it
// while holding another file lock — use lockFiles for multiple paths.
func lockFile(imgPath string) *sync.Mutex {
	mu := &fileLocks[fileStripe(imgPath)]
	mu.Lock()
	return mu
}

// lockFiles locks every path's stripe in ascending stripe order, taking each
// stripe once, so callers locking several paths can neither deadlock against
// each other nor self-deadlock when two paths share a stripe. It returns the
// unlock function.
func lockFiles(paths ...string) (unlock func()) {
	stripes := make([]int, 0, len(paths))
	for _, p := range paths {
		stripes = append(stripes, fileStripe(p))
	}
	sort.Ints(stripes)
	held := stripes[:0]
	for i, s := range stripes {
		if i > 0 && s == stripes[i-1] {
			continue
		}
		fileLocks[s].Lock()
		held = append(held, s)
	}
	return func() {
		for i := len(held) - 1; i >= 0; i-- {
			fileLocks[held[i]].Unlock()
		}
	}
}

// maxImagePixels caps the pixel count of any image we fully decode. A tiny
// file can declare enormous dimensions (a decompression bomb); decoding costs
// roughly 4–6 bytes per pixel, so the default 100 MP is ~500 MB. Set from the
// -max-megapixels flag.
var maxImagePixels int64 = 100_000_000

var errImageTooLarge = errors.New("image exceeds pixel limit")

func checkPixelLimit(w, h int) error {
	if int64(w)*int64(h) > maxImagePixels {
		return fmt.Errorf("%w: %dx%d", errImageTooLarge, w, h)
	}
	return nil
}

// checkSourcePixels reads only the image header of srcPath and rejects it if
// it exceeds the pixel limit. Unreadable headers pass: the full decode that
// follows will fail on its own (and the ffmpeg fallback checks its output).
func checkSourcePixels(srcPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil
	}
	return checkPixelLimit(cfg.Width, cfg.Height)
}

// maxConcurrentDecodes bounds full image decodes process-wide — warmup,
// on-demand thumbnails, uploads and crops all share decodeSem — so a burst of
// requests can't exhaust memory. Warmup uses at most 2 of these slots, which
// keeps at least one free for interactive requests.
const maxConcurrentDecodes = 3

var decodeSem = make(chan struct{}, maxConcurrentDecodes)

// acquireDecode waits for a decode slot, giving up if ctx is cancelled (e.g.
// the client scrolled past the thumbnail and dropped the request). Lock order
// is always per-file mutex first, then decode slot.
func acquireDecode(ctx context.Context) error {
	select {
	case decodeSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseDecode() { <-decodeSem }

// openSourceImage decodes the photo at imgPath (auto-oriented), refusing
// images over the pixel limit; videos yield one extracted frame. When the
// stdlib decoder rejects a file, ffmpeg is tried; if repair is set the
// ffmpeg-decoded pixels are written back over the original (JPEGs only — see
// repairOriginalJPEG). Callers must hold a decode slot.
func openSourceImage(imgPath string, repair bool, logPrefix string) (image.Image, error) {
	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))
	if isVideo(imgPath) {
		return extractVideoFrame(srcPath)
	}
	if err := checkSourcePixels(srcPath); err != nil {
		return nil, err
	}
	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err == nil {
		return img, nil
	}
	fb, ferr := decodeJPEGFallback(srcPath)
	if ferr != nil {
		if errors.Is(ferr, errImageTooLarge) {
			return nil, ferr
		}
		return nil, err
	}
	log.Printf("%s: stdlib decode failed for %s (%v); used ffmpeg fallback", logPrefix, imgPath, err)
	if repair {
		switch rerr := repairOriginalJPEG(srcPath, fb); {
		case rerr == nil:
			log.Printf("%s: repaired %s in place", logPrefix, imgPath)
		case errors.Is(rerr, errNotJPEG):
		case errors.Is(rerr, errReadOnlyPhotos):
			log.Printf("%s: repair %s skipped: photos dir not writable", logPrefix, imgPath)
		default:
			log.Printf("%s: repair %s failed: %v", logPrefix, imgPath, rerr)
		}
	}
	return fb, nil
}

// writeThumb JPEG-encodes img and atomically writes it to the cache path cp,
// returning the encoded bytes.
func writeThumb(cp string, img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	return data, writeFileAtomic(cp, data, 0o644, false)
}

// mediumImage downsizes img to mediumWidth when wider, else returns it as is.
func mediumImage(img image.Image) image.Image {
	if img.Bounds().Dx() > mediumWidth {
		return imaging.Resize(img, mediumWidth, 0, imaging.Lanczos)
	}
	return img
}

func smallImage(img image.Image) image.Image {
	return imaging.Fit(img, thumbnailSize, thumbnailSize, imaging.Lanczos)
}

// writeThumbs caches the requested thumbnail sizes for imgPath from one
// decoded image and returns the encoded bytes of each size written. When both
// are wanted the small thumb is downscaled from the medium one rather than the
// full-resolution source, which is several times cheaper. A non-nil error
// reports the first failure; data for sizes that encoded is still returned.
func writeThumbs(imgPath string, img image.Image, small, medium bool) (smallData, mediumData []byte, err error) {
	src := img
	if medium {
		med := mediumImage(img)
		mediumData, err = writeThumb(thumbMediumCachePath(imgPath), med, 90)
		src = med
	}
	if small {
		var serr error
		smallData, serr = writeThumb(thumbSmallCachePath(imgPath), smallImage(src), 85)
		if err == nil {
			err = serr
		}
	}
	return smallData, mediumData, err
}

// cacheFileReady reports whether a non-empty cached thumbnail exists at cp.
// Zero-length files (e.g. left by a power loss before data hit the disk) are
// treated as missing so they get regenerated.
func cacheFileReady(cp string) bool {
	fi, err := os.Stat(cp)
	return err == nil && fi.Size() > 0
}

// thumbHash returns a 16-char hex string that changes whenever the source
// file's OS mtime changes, ensuring browsers fetch a fresh thumbnail after
// an external edit.
func thumbHash(filename string, mtime time.Time) string {
	h := sha256.Sum256([]byte(filename + "\x00" + strconv.FormatInt(mtime.UnixNano(), 10)))
	return hex.EncodeToString(h[:8])
}

// cacheControlForHashedURL returns the immutable cache header when urlHash
// matches the current (path, mtime) hash for imgPath. On mismatch (a stale
// shared URL after a crop / re-upload), or when the source can't be stat'd,
// it returns a short cache so the recipient's browser doesn't pin outdated
// bytes for a year.
func cacheControlForHashedURL(imgPath, urlHash string) string {
	if urlHash == "" {
		return "public, max-age=3600"
	}
	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))
	if fi, err := os.Stat(srcPath); err == nil && thumbHash(imgPath, fi.ModTime()) == urlHash {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600"
}

func thumbSmallCachePath(imgPath string) string {
	return filepath.Join(cacheDir, "s", filepath.FromSlash(imgPath))
}

func thumbMediumCachePath(imgPath string) string {
	return filepath.Join(cacheDir, "m", filepath.FromSlash(imgPath))
}

// thumbURLs returns the API URLs for the small thumbnail, medium thumbnail,
// and original of imgPath. mtime is the file's OS modification time used
// for cache-busting.
func thumbURLs(imgPath string, mtime time.Time) (small, medium, original string) {
	h := thumbHash(imgPath, mtime)
	enc := encodePathSegments(imgPath)
	return "/api/thumb/" + h + "/" + enc,
		"/api/thumb-medium/" + h + "/" + enc,
		"/api/original/" + h + "/" + enc
}

func parseThumbPath(prefix, fullPath string) (hash, imgPath string, ok bool) {
	rest := strings.TrimPrefix(fullPath, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return
	}
	hash = rest[:slash]
	imgPath = rest[slash+1:]
	if hash == "" || !isValidPath(imgPath) || !isSupportedMedia(imgPath) {
		return
	}
	ok = true
	return
}

// serveCachedThumb is the shared implementation for small and medium thumbnail
// handlers. All sizes and crop share the same per-filename mutex so a crop
// cannot race with a concurrent thumbnail write for the same file. On a cache
// miss it also generates the other size if missing, so one source decode
// serves both the grid and the lightbox.
func serveCachedThumb(w http.ResponseWriter, r *http.Request, prefix string, medium bool) {
	urlHash, imgPath, ok := parseThumbPath(prefix, r.URL.Path)
	if !ok {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if !userCanAccessPath(userFromCtx(r), imgPath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cp, otherCP := thumbSmallCachePath(imgPath), thumbMediumCachePath(imgPath)
	if medium {
		cp, otherCP = otherCP, cp
	}
	cc := cacheControlForHashedURL(imgPath, urlHash)

	if cacheFileReady(cp) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", cc)
		http.ServeFile(w, r, cp)
		return
	}

	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))
	if _, err := os.Stat(srcPath); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	mu := lockFile(imgPath)
	defer mu.Unlock()

	if cacheFileReady(cp) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", cc)
		http.ServeFile(w, r, cp)
		return
	}

	if err := acquireDecode(r.Context()); err != nil {
		return // client went away while queued
	}
	defer releaseDecode()

	img, err := openSourceImage(imgPath, true, "thumb")
	if err != nil {
		log.Printf("thumb: decode %s: %v", imgPath, err)
		if errors.Is(err, errImageTooLarge) {
			http.Error(w, "image too large", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "failed to decode image", http.StatusInternalServerError)
		return
	}
	b := img.Bounds()
	needOther := !cacheFileReady(otherCP)
	smallData, mediumData, err := writeThumbs(imgPath, img, !medium || needOther, medium || needOther)
	data := smallData
	if medium {
		data = mediumData
	}
	if err != nil {
		log.Printf("thumb: %s: %v", imgPath, err)
	}
	if data == nil { // a failed cache write alone still returns data to serve
		http.Error(w, "failed to encode thumbnail", http.StatusInternalServerError)
		return
	}
	indexFileRecord(imgPath, b.Dx(), b.Dy())

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", cc)
	w.Write(data) //nolint:errcheck
}

func handleThumb(w http.ResponseWriter, r *http.Request) {
	serveCachedThumb(w, r, "/api/thumb/", false)
}

func handleThumbMedium(w http.ResponseWriter, r *http.Request) {
	// Medium thumbs are only requested when the lightbox opens, so a hit here
	// is a reliable proxy for "user viewed this file". Small thumbs are used
	// for grid scrolling and are deliberately not tracked.
	if _, imgPath, ok := parseThumbPath("/api/thumb-medium/", r.URL.Path); ok {
		if userCanAccessPath(userFromCtx(r), imgPath) {
			u := userFromCtx(r)
			safeGo("trackView-thumb-medium", func() { trackView(imgPath, u) })
		}
	}
	serveCachedThumb(w, r, "/api/thumb-medium/", true)
}

// warmupRec holds the database fields needed by warmupThumbnails to decide
// whether a file needs re-processing without a per-file query.
type warmupRec struct {
	fileMtime time.Time
	width     int
}

func warmupThumbnails() {
	log.Println("warmup: cache pre-warming started")

	prev := debug.SetGCPercent(20)
	defer func() {
		debug.SetGCPercent(prev)
		debug.FreeOSMemory()
		log.Println("warmup: all thumbnails ready")
	}()

	// Pre-load all stored records so we avoid per-file DB queries in the walk.
	existing := make(map[string]warmupRec)
	if rows, err := readDB().Query(`SELECT path, file_mtime, width FROM files`); err == nil {
		for rows.Next() {
			var p string
			var ns int64
			var w int
			if rows.Scan(&p, &ns, &w) == nil {
				existing[p] = warmupRec{fileMtime: time.Unix(0, ns), width: w}
			}
		}
		rows.Close()
	}

	semFull := make(chan struct{}, 2)
	semLight := make(chan struct{}, 8)
	var wg sync.WaitGroup

	walkPhotosDir(func(imgPath string, fi os.FileInfo) {
		rec := existing[imgPath]
		mtimeChanged := !rec.fileMtime.Equal(fi.ModTime())
		hasDims := rec.width > 0

		needsSmall := !cacheFileReady(thumbSmallCachePath(imgPath))
		needsMedium := !cacheFileReady(thumbMediumCachePath(imgPath))

		if hasDims && !needsSmall && !needsMedium && !mtimeChanged {
			return
		}

		// Thumbnails already on disk, mtime unchanged — only dims are missing.
		// Use DecodeConfig to read just the image header instead of full decode.
		if !needsSmall && !needsMedium && !mtimeChanged && !isVideo(imgPath) {
			wg.Add(1)
			semLight <- struct{}{}
			go func(p string) {
				defer wg.Done()
				defer func() { <-semLight }()
				srcPath := filepath.Join(photosDir, filepath.FromSlash(p))
				f, err := os.Open(srcPath)
				if err != nil {
					return
				}
				defer f.Close()
				cfg, _, err := image.DecodeConfig(f)
				if err != nil {
					indexFileRecord(p, 0, 0)
					return
				}
				w, h := cfg.Width, cfg.Height
				f.Seek(0, io.SeekStart) //nolint:errcheck
				if x, err := exif.Decode(f); err == nil {
					if tag, err := x.Get(exif.Orientation); err == nil {
						if o, err := tag.Int(0); err == nil && o >= 5 && o <= 8 {
							w, h = h, w
						}
					}
				}
				indexFileRecord(p, w, h)
			}(imgPath)
			return
		}

		wg.Add(1)
		semFull <- struct{}{}
		go func(p string, genSmall, genMedium bool) {
			defer wg.Done()
			defer func() { <-semFull }()

			// Same lock as crop/upload/thumbnail requests, so warmup can't
			// write a thumbnail of pre-crop pixels after a crop evicted it.
			mu := lockFile(p)
			defer mu.Unlock()
			acquireDecode(context.Background()) //nolint:errcheck
			defer releaseDecode()

			img, err := openSourceImage(p, true, "warmup")
			if err != nil {
				log.Printf("warmup: open %s: %v", p, err)
				indexFileRecord(p, 0, 0)
				return
			}
			b := img.Bounds()
			indexFileRecord(p, b.Dx(), b.Dy())
			if _, _, err := writeThumbs(p, img, genSmall, genMedium); err != nil {
				log.Printf("warmup: thumbs %s: %v", p, err)
			}
		}(imgPath, needsSmall || mtimeChanged, needsMedium || mtimeChanged)
	})
	wg.Wait()
}
