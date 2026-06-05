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

// thumbHash returns a 16-char hex string that changes whenever the source
// file's OS mtime changes, ensuring browsers fetch a fresh thumbnail after
// an external edit.
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
	indexFileRecord(imgPath, b.Dx(), b.Dy())

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
	if rows, err := db.Query(`SELECT path, file_mtime, width FROM files`); err == nil {
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
				indexFileRecord(p, 0, 0)
				return
			}
			b := img.Bounds()
			indexFileRecord(p, b.Dx(), b.Dy())
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
}
