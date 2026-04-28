package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

const thumbnailSize = 400

// Paths and port are set from flags in main(); handlers use these vars.
var (
	photosDir  string
	cacheDir   string
	serverPort string
)

var supportedExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
}

type ImageInfo struct {
	Filename string    `json:"filename"`
	ModTime  time.Time `json:"modTime"`
	Size     int64     `json:"size"`
}

type ListResponse struct {
	Images []ImageInfo `json:"images"`
	Total  int         `json:"total"`
	Page   int         `json:"page"`
	Limit  int         `json:"limit"`
}

// thumbMu provides per-filename locking to avoid duplicate concurrent generation.
var thumbMu sync.Map

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleImages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortBy := q.Get("sort")   // "name" | "date"
	order := q.Get("order")   // "asc"  | "desc"
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}

	entries, err := os.ReadDir(photosDir)
	if err != nil {
		http.Error(w, "cannot read photos directory", http.StatusInternalServerError)
		return
	}

	var images []ImageInfo
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
		images = append(images, ImageInfo{
			Filename: e.Name(),
			ModTime:  fi.ModTime(),
			Size:     fi.Size(),
		})
	}

	switch sortBy {
	case "date":
		if order == "asc" {
			sort.Slice(images, func(i, j int) bool { return images[i].ModTime.Before(images[j].ModTime) })
		} else {
			sort.Slice(images, func(i, j int) bool { return images[i].ModTime.After(images[j].ModTime) })
		}
	default: // name
		if order == "desc" {
			sort.Slice(images, func(i, j int) bool { return images[i].Filename > images[j].Filename })
		} else {
			sort.Slice(images, func(i, j int) bool { return images[i].Filename < images[j].Filename })
		}
	}

	total := len(images)
	start := (page - 1) * limit
	end := start + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListResponse{
		Images: images[start:end],
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

// thumbnailCachePath returns the cache path for a given original filename.
// The full filename is encoded into the cache name to avoid base-name collisions.
func thumbnailCachePath(filename string) string {
	// Replace path separators that could sneak in, then append .thumb.jpg
	safe := strings.ReplaceAll(filename, string(filepath.Separator), "_")
	return filepath.Join(cacheDir, safe+".thumb.jpg")
}

func handleThumbnail(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/api/thumbnail/")
	if filename == "" || strings.ContainsAny(filename, "/\\") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	srcPath := filepath.Join(photosDir, filename)
	cachePath := thumbnailCachePath(filename)

	// Per-filename mutex to avoid generating the same thumbnail concurrently.
	rawMu, _ := thumbMu.LoadOrStore(filename, &sync.Mutex{})
	mu := rawMu.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		http.Error(w, "image not found", http.StatusNotFound)
		return
	}

	// Serve from cache if it exists and is newer than the source.
	if cacheInfo, err := os.Stat(cachePath); err == nil && cacheInfo.ModTime().After(srcInfo.ModTime()) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		http.ServeFile(w, r, cachePath)
		return
	}

	img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		http.Error(w, "failed to decode image", http.StatusInternalServerError)
		return
	}

	thumb := imaging.Thumbnail(img, thumbnailSize, thumbnailSize, imaging.Lanczos)

	// Write to cache (best-effort; failure does not break the response).
	if f, err := os.Create(cachePath); err == nil {
		if encErr := jpeg.Encode(f, thumb, &jpeg.Options{Quality: 85}); encErr != nil {
			log.Printf("warn: cache encode %s: %v", cachePath, encErr)
		}
		f.Close()
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	jpeg.Encode(w, thumb, &jpeg.Options{Quality: 85}) //nolint:errcheck
}

func handleOriginal(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/api/original/")
	if filename == "" || strings.ContainsAny(filename, "/\\") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, filepath.Join(photosDir, filename))
}

// handleUpload receives a single image file from the frontend ShareUploader
// component (POST /api/upload, field name "file") and saves it to photosDir.
// The frontend uploads each shared file individually so it can track progress.
func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Allow up to 100 MB per file; the rest stays on disk as a temp file.
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

	// Strip any directory component to prevent path traversal.
	safeName := filepath.Base(header.Filename)
	destPath := filepath.Join(photosDir, safeName)

	// Avoid overwriting an existing file by appending a millisecond timestamp.
	if _, err := os.Stat(destPath); err == nil {
		base := strings.TrimSuffix(safeName, ext)
		safeName = fmt.Sprintf("%s_%d%s", base, time.Now().UnixMilli(), ext)
		destPath = filepath.Join(photosDir, safeName)
	}

	dst, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "failed to create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	log.Printf("upload: saved %s", destPath)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"filename": safeName})
}

// warmupThumbnails pre-generates thumbnails for all photos in the background
// using a pool of 4 concurrent workers.
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
		if _, err := os.Stat(thumbnailCachePath(name)); err == nil {
			continue // already cached
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(filename string) {
			defer wg.Done()
			defer func() { <-sem }()

			srcPath := filepath.Join(photosDir, filename)
			img, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
			if err != nil {
				log.Printf("warmup: open %s: %v", filename, err)
				return
			}
			thumb := imaging.Thumbnail(img, thumbnailSize, thumbnailSize, imaging.Lanczos)
			cp := thumbnailCachePath(filename)
			if f, err := os.Create(cp); err == nil {
				jpeg.Encode(f, thumb, &jpeg.Options{Quality: 85}) //nolint:errcheck
				f.Close()
			}
		}(name)
	}
	wg.Wait()
	log.Println("warmup: all thumbnails ready")
}

func main() {
	flag.StringVar(&photosDir, "photos", "./photos", "directory containing source images")
	flag.StringVar(&cacheDir, "cache", "./cache", "directory for thumbnail cache")
	flag.StringVar(&serverPort, "port", ":8080", "listen address")
	flag.Parse()

	// Make paths absolute so they are stable regardless of later chdir.
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

	go warmupThumbnails()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/images", handleImages)
	mux.HandleFunc("/api/thumbnail/", handleThumbnail)
	mux.HandleFunc("/api/original/", handleOriginal)
	mux.HandleFunc("/api/upload", handleUpload)
	// Serve the compiled frontend; in development Vite's proxy handles /api.
	mux.Handle("/", http.FileServer(http.Dir("./frontend/dist")))

	log.Printf("listening on %s", serverPort)
	log.Fatal(http.ListenAndServe(serverPort, corsMiddleware(mux)))
}
