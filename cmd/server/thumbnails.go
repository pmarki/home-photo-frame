package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"
)

const thumbnailSize = 400

var fileMu sync.Map

// thumbHash returns a 16-char hex string that uniquely identifies the content
// of a thumbnail: it changes whenever the source file's OS mtime changes,
// ensuring browsers fetch a fresh thumbnail after an external edit.
func thumbHash(filename string, mtime time.Time) string {
	h := sha256.Sum256([]byte(filename + "\x00" + strconv.FormatInt(mtime.UnixNano(), 10)))
	return hex.EncodeToString(h[:8])
}

func thumbSmallCachePath(imgPath string) string {
	return filepath.Join(cacheDir, "s", filepath.FromSlash(imgPath))
}

func thumbMediumCachePath(imgPath string) string {
	return filepath.Join(cacheDir, "m", filepath.FromSlash(imgPath))
}

// thumbURLs returns the API URLs for the small thumbnail, medium thumbnail, and
// original of imgPath. It reads FileMtime from metaIndex; if not yet recorded
// it falls back to os.Stat. The hash in each URL changes when the source file's
// mtime changes, ensuring browsers fetch fresh content after an edit.
func thumbURLs(imgPath string) (small, medium, original string) {
	metaMu.RLock()
	meta := metaIndex[imgPath]
	metaMu.RUnlock()
	mtime := meta.FileMtime
	if mtime.IsZero() {
		if fi, err := os.Stat(filepath.Join(photosDir, filepath.FromSlash(imgPath))); err == nil {
			mtime = fi.ModTime()
		}
	}
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
	imgPath = rest[slash+1:] // r.URL.Path is already decoded by net/http; path may contain "/"
	if hash == "" || !isValidPath(imgPath) {
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
	_, imgPath, ok := parseThumbPath(prefix, r.URL.Path)
	if !ok {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	cp := cachePath(imgPath)

	// Cache hit: URL is content-addressed, safe to cache forever.
	if _, err := os.Stat(cp); err == nil {
		serveImmutable(w, r, cp)
		return
	}

	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))
	if _, err := os.Stat(srcPath); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	rawMu, _ := fileMu.LoadOrStore(imgPath, &sync.Mutex{})
	mu := rawMu.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	if _, err := os.Stat(cp); err == nil {
		serveImmutable(w, r, cp)
		return
	}

	var img image.Image
	var err error
	if isVideo(imgPath) {
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
	os.MkdirAll(filepath.Dir(cp), 0o755) //nolint:errcheck
	if f, err := os.Create(cp); err == nil {
		if _, werr := f.Write(data); werr != nil {
			f.Close()
			os.Remove(cp)
		} else {
			f.Close()
		}
	}
	indexImage(imgPath, b.Dx(), b.Dy())
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

func warmupThumbnails() {
	log.Println("warmup: cache pre-warming started")

	// Tighter GC target keeps peak heap lower when many large image buffers are
	// live concurrently. Restored (with an OS-level free) once warmup finishes.
	prev := debug.SetGCPercent(20)
	defer func() {
		debug.SetGCPercent(prev)
		debug.FreeOSMemory()
		log.Println("warmup: all thumbnails ready")
	}()

	// semFull bounds concurrent full-image decodes (each may hold tens of MB).
	// semLight bounds header-only reads (DecodeConfig), which are cheap.
	semFull := make(chan struct{}, 2)
	semLight := make(chan struct{}, 8)
	var wg sync.WaitGroup

	walkPhotosDir(func(imgPath string, fi os.FileInfo) {
		fileMtime := fi.ModTime()

		metaMu.RLock()
		meta := metaIndex[imgPath]
		metaMu.RUnlock()

		mtimeChanged := !meta.FileMtime.Equal(fileMtime)
		hasDims := meta.Width > 0

		needsSmall := func() bool {
			_, err := os.Stat(thumbSmallCachePath(imgPath))
			return err != nil
		}()
		needsMedium := func() bool {
			_, err := os.Stat(thumbMediumCachePath(imgPath))
			return err != nil
		}()

		if hasDims && !needsSmall && !needsMedium && !mtimeChanged {
			return
		}

		// Thumbnails already on disk, mtime unchanged, only dims are missing
		// (e.g. meta.json was cleared). Read just the image header instead of
		// decoding megapixels into RAM.
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
					indexImage(p, 0, 0)
					return
				}
				w, h := cfg.Width, cfg.Height
				// DecodeConfig ignores EXIF orientation; swap dims for 90°/270° images.
				f.Seek(0, io.SeekStart) //nolint:errcheck
				if x, err := exif.Decode(f); err == nil {
					if tag, err := x.Get(exif.Orientation); err == nil {
						if o, err := tag.Int(0); err == nil && o >= 5 && o <= 8 {
							w, h = h, w
						}
					}
				}
				indexImage(p, w, h)
			}(imgPath)
			return
		}

		wg.Add(1)
		semFull <- struct{}{}
		go func(p string, genSmall, genMedium bool) {
			defer wg.Done()
			defer func() { <-semFull }()

			srcPath := filepath.Join(photosDir, filepath.FromSlash(p))
			var img image.Image
			var err error
			if isVideo(p) {
				img, err = extractVideoFrame(srcPath)
			} else {
				img, err = imaging.Open(srcPath, imaging.AutoOrientation(true))
			}
			if err != nil {
				log.Printf("warmup: open %s: %v", p, err)
				indexImage(p, 0, 0)
				return
			}
			b := img.Bounds()
			indexImage(p, b.Dx(), b.Dy())
			if genSmall {
				thumb := imaging.Fit(img, thumbnailSize, thumbnailSize, imaging.Lanczos)
				cp := thumbSmallCachePath(p)
				os.MkdirAll(filepath.Dir(cp), 0o755) //nolint:errcheck
				if f, err := os.Create(cp); err == nil {
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
				img = nil
				cp := thumbMediumCachePath(p)
				os.MkdirAll(filepath.Dir(cp), 0o755) //nolint:errcheck
				if f, err := os.Create(cp); err == nil {
					jpeg.Encode(f, thumb, &jpeg.Options{Quality: 90}) //nolint:errcheck
					f.Close()
				}
			} else {
				img = nil
			}
		}(imgPath, needsSmall || mtimeChanged, needsMedium || mtimeChanged)
	})
	wg.Wait()
	saveMetaIndex()
}
