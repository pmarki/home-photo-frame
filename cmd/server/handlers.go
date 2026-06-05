package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
)

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
	if limit < 1 || limit > 10000 {
		limit = 50
	}

	key := sortBy + ":" + order

	// buildPage extracts a page of ImageInfo from a sorted index slice.
	// Must be called with imagesCacheMu held.
	buildPage := func(indices []int) ([]ImageInfo, int) {
		total := len(indices)
		if !paginate {
			out := make([]ImageInfo, total)
			for i, idx := range indices {
				out[i] = imagesCache[idx]
			}
			return out, total
		}
		start := (page - 1) * limit
		end := start + limit
		if start > total {
			start = total
		}
		if end > total {
			end = total
		}
		out := make([]ImageInfo, end-start)
		for i, idx := range indices[start:end] {
			out[i] = imagesCache[idx]
		}
		return out, total
	}

	var slice []ImageInfo
	var total int

	// Fast path: sorted index is already cached.
	imagesCacheMu.RLock()
	indices, hit := sortedCache[key]
	if hit {
		slice, total = buildPage(indices)
	}
	imagesCacheMu.RUnlock()

	if !hit {
		// Slow path: build and cache the sorted index.
		imagesCacheMu.Lock()
		if indices, hit = sortedCache[key]; !hit {
			indices = make([]int, len(imagesCache))
			for i := range indices {
				indices[i] = i
			}
			sortIndices(indices, imagesCache, sortBy, order)
			if sortedCache == nil {
				sortedCache = make(map[string][]int)
			}
			sortedCache[key] = indices
		}
		slice, total = buildPage(indices)
		imagesCacheMu.Unlock()
	}

	if !paginate {
		page = 1
		limit = total
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(ListResponse{ //nolint:errcheck
		Images: slice,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

func handleOriginal(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/original/")
	slash := strings.IndexByte(rest, '/')
	var imgPath, cacheControl string
	if slash < 0 {
		imgPath = rest
		cacheControl = "public, max-age=3600"
	} else {
		imgPath = rest[slash+1:]
		cacheControl = "public, max-age=31536000, immutable"
	}
	if !isValidPath(imgPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeFile(w, r, filepath.Join(photosDir, filepath.FromSlash(imgPath)))
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
		json.NewEncoder(w).Encode(map[string]string{"status": "chunk_ok"}) //nolint:errcheck
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
			Path:        safeName, // uploads go to root; path == filename
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
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"filename":    safeName,
		"path":        safeName,
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
	imgPath := strings.TrimPrefix(r.URL.Path, "/api/delete/")
	if !isValidPath(imgPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))

	if err := os.Remove(srcPath); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to delete", http.StatusInternalServerError)
		}
		return
	}
	os.Remove(thumbSmallCachePath(imgPath))
	os.Remove(thumbMediumCachePath(imgPath))
	fileMu.Delete(imgPath)
	metaMu.Lock()
	delete(metaIndex, imgPath)
	metaMu.Unlock()
	cacheRemove(imgPath)
	saveMetaIndex()
	log.Printf("delete: %s", srcPath)
	w.WriteHeader(http.StatusNoContent)
}

func handleCrop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	imgPath := strings.TrimPrefix(r.URL.Path, "/api/crop/")
	if !isValidPath(imgPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
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

	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))

	// Serialize all operations on this file through its per-file mutex.
	rawMu, _ := fileMu.LoadOrStore(imgPath, &sync.Mutex{})
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
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > bounds.Dx() {
		x1 = bounds.Dx()
	}
	if y1 > bounds.Dy() {
		y1 = bounds.Dy()
	}
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
	os.Remove(thumbSmallCachePath(imgPath))
	os.Remove(thumbMediumCachePath(imgPath))
	indexImage(imgPath, b.Dx(), b.Dy())
	saveMetaIndex()

	fi, statErr := os.Stat(srcPath)
	small, medium, original := thumbURLs(imgPath)

	imagesCacheMu.Lock()
	for i, entry := range imagesCache {
		if entry.Path == imgPath {
			if statErr == nil {
				imagesCache[i].Size = fi.Size()
				imagesCache[i].FileMtime = fi.ModTime()
				imagesCache[i].ModTime = bestDate(imgPath, fi.ModTime())
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

	log.Printf("crop: %s (%dx%d)", imgPath, b.Dx(), b.Dy())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"filename":    filepath.Base(imgPath),
		"path":        imgPath,
		"width":       b.Dx(),
		"height":      b.Dy(),
		"thumbSmall":  small,
		"thumbMedium": medium,
		"original":    original,
	})
}

// handleConfig returns runtime configuration consumed by the Vue frontend.
func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	titleIcon := iconsDir != ""
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"title":        appTitle,
		"videoEnabled": videoEnabled,
		"bgColor":      bgColor,
		"titleIcon":    titleIcon,
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
