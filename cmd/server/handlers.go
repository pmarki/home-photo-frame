package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/disintegration/imaging"
)

func handleImages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	paginate := q.Has("limit")
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 10000 {
		limit = 50
	}

	u := userFromCtx(r)
	folder := q.Get("folder")
	if folder != "" && !isValidPath(folder) {
		http.Error(w, "invalid folder", http.StatusBadRequest)
		return
	}
	if folder != "" && !userCanAccessFolder(u, folder) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	year, _ := strconv.Atoi(q.Get("year"))

	var ownerFolders []string
	if ownerID := q.Get("owner"); ownerID != "" {
		// Non-nil empty slice forces an empty result when the owner exists but
		// owns nothing (or doesn't exist at all). queryFiles distinguishes nil
		// (no filter) from empty (always-false).
		ownerFolders = []string{}
		for folder, owners := range folderOwners {
			for _, id := range owners {
				if id == ownerID {
					ownerFolders = append(ownerFolders, folder)
					break
				}
			}
		}
	}

	viewed := q.Get("viewed")
	switch viewed {
	case "", "recent", "most":
	default:
		http.Error(w, "invalid viewed", http.StatusBadRequest)
		return
	}
	uid := ""
	if u != nil {
		uid = u.ID
	}

	favorited := q.Get("favorited") == "1" || strings.EqualFold(q.Get("favorited"), "true")

	params := queryParams{
		folder:           folder,
		ftype:            q.Get("type"),
		search:           q.Get("search"),
		year:             year,
		ownerFolders:     ownerFolders,
		sort:             q.Get("sort"),
		order:            q.Get("order"),
		page:             page,
		limit:            limit,
		paginate:         paginate,
		deniedTopFolders: deniedTopFoldersFor(u),
		viewedMode:       viewed,
		favorited:        favorited,
		userID:           uid,
	}

	images, total, err := queryFiles(params)
	if err != nil {
		log.Printf("handleImages: query error: %v", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	if images == nil {
		images = []ImageInfo{}
	}
	if !paginate {
		page = 1
		limit = total
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(ListResponse{ //nolint:errcheck
		Images: images,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

func handleOriginal(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/original/")
	slash := strings.IndexByte(rest, '/')
	var urlHash, imgPath string
	if slash < 0 {
		imgPath = rest
	} else {
		urlHash = rest[:slash]
		imgPath = rest[slash+1:]
	}
	if !isValidPath(imgPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	// Only serve indexed media types — never temp files, sidecars or
	// anything else that happens to sit in the photos directory.
	if !isSupportedMedia(imgPath) {
		http.NotFound(w, r)
		return
	}
	u := userFromCtx(r)
	if !userCanAccessPath(u, imgPath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	safeGo("trackView-original", func() { trackView(imgPath, u) })
	w.Header().Set("Cache-Control", cacheControlForHashedURL(imgPath, urlHash))
	http.ServeFile(w, r, filepath.Join(photosDir, filepath.FromSlash(imgPath)))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}
	if id := r.FormValue("uploadId"); id != "" {
		handleChunkedUpload(w, r, id)
		return
	}
	folder := r.FormValue("folder")
	if folder != "" && !isValidPath(folder) {
		http.Error(w, "invalid folder", http.StatusBadRequest)
		return
	}
	if !userCanAccessFolder(userFromCtx(r), folder) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	src, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer src.Close()
	saveUploadedFile(r.Context(), w, src, header.Filename, folder)
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
	folder := r.FormValue("folder")
	if folder != "" && !isValidPath(folder) {
		http.Error(w, "invalid folder", http.StatusBadRequest)
		return
	}
	if !userCanAccessFolder(userFromCtx(r), folder) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
	saveUploadedFile(r.Context(), w, asm, filename, folder)
	asm.Close()
	os.RemoveAll(tmpDir)
}

func saveUploadedFile(ctx context.Context, w http.ResponseWriter, src io.Reader, originalName, folder string) {
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
	imgPath := safeName
	if folder != "" {
		imgPath = folder + "/" + safeName
	}
	destPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))
	// Defense in depth: ensure the resolved destination stays inside photosDir.
	if rel, err := filepath.Rel(photosDir, destPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		http.Error(w, "destination outside photos directory", http.StatusBadRequest)
		return
	}
	if folder != "" {
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			log.Printf("upload: mkdir %s: %v", filepath.Dir(destPath), err)
			http.Error(w, "failed to create folder", http.StatusInternalServerError)
			return
		}
	}

	// Hold the per-file mutex across the write + inline thumb + index so a
	// concurrent thumbnail request can't open a half-written file.
	mu := lockFile(imgPath)
	defer mu.Unlock()

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

	var imgW, imgH int
	if !isVideo(safeName) {
		// Reject decompression bombs up front: such a file would never get a
		// thumbnail and would be re-attempted by every warmup.
		if err := checkSourcePixels(destPath); errors.Is(err, errImageTooLarge) {
			os.Remove(destPath)
			log.Printf("upload: rejected %s: %v", destPath, err)
			http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := acquireDecode(ctx); err == nil {
			srcImg, derr := openSourceImage(imgPath, false, "upload")
			if derr == nil {
				b := srcImg.Bounds()
				imgW, imgH = b.Dx(), b.Dy()
				// Both sizes from the same decoded image — avoids a second
				// full decode on the first lightbox view.
				if _, _, err := writeThumbs(imgPath, srcImg, true, true); err != nil {
					log.Printf("upload: thumbs %s: %v", imgPath, err)
				}
			}
			releaseDecode()
		}
	}

	indexFileRecord(imgPath, imgW, imgH)

	fi, err := os.Stat(destPath)
	if err != nil {
		http.Error(w, "failed to stat uploaded file", http.StatusInternalServerError)
		return
	}
	small, medium, original := thumbURLs(imgPath, fi.ModTime())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"filename":    safeName,
		"path":        imgPath,
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
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(body.Paths) == 0 {
		http.Error(w, "no paths", http.StatusBadRequest)
		return
	}
	u := userFromCtx(r)
	for _, p := range body.Paths {
		if isValidPath(p) && !userCanAccessPath(u, p) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	deleted := []string{}
	failed := []failedItem{}

	for _, imgPath := range body.Paths {
		if !isValidPath(imgPath) {
			failed = append(failed, failedItem{Path: imgPath, Error: "invalid path"})
			continue
		}
		if msg := deleteOne(imgPath); msg != "" {
			failed = append(failed, failedItem{Path: imgPath, Error: msg})
			continue
		}
		deleted = append(deleted, imgPath)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"deleted": deleted,
		"failed":  failed,
	})
}

// deleteOne removes one photo with its cached thumbnails and records, under
// the file lock so a concurrent thumbnail request can't re-create a cache
// file for it. Returns "" on success or the per-item error message.
func deleteOne(imgPath string) string {
	mu := lockFile(imgPath)
	defer mu.Unlock()
	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))
	if err := os.Remove(srcPath); err != nil {
		if os.IsNotExist(err) {
			return "not found"
		}
		log.Printf("delete: %s: %v", srcPath, err)
		return "delete failed"
	}
	os.Remove(thumbSmallCachePath(imgPath))
	os.Remove(thumbMediumCachePath(imgPath))
	deleteFile(imgPath)
	log.Printf("delete: %s", srcPath)
	return ""
}

// resolveMoveCopyDestination computes the destination path for a single source
// in a move/copy operation, validates it, and returns the absolute and
// relative target paths. Returns a non-empty error string when the operation
// should be reported as failed for this item.
// failedItem reports one path that a bulk delete/move/copy could not process.
type failedItem struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func resolveMoveCopyDestination(src, destination string) (dstRel, dstAbs string, errMsg string) {
	if !isValidPath(src) {
		return "", "", "invalid path"
	}
	base := filepath.Base(src)
	if destination == "" {
		dstRel = base
	} else {
		dstRel = destination + "/" + base
	}
	if !isValidPath(dstRel) {
		return "", "", "invalid destination"
	}
	if dstRel == src {
		return "", "", "already at destination"
	}
	dstAbs = filepath.Join(photosDir, filepath.FromSlash(dstRel))
	// Defense in depth: ensure the resolved absolute path stays inside photosDir.
	// isValidPath already rejects ".." components in the user-supplied destination,
	// but this guards against any future change to that helper, against
	// platform-specific quirks in filepath.Join, and against accidental
	// concatenation bugs.
	rel, err := filepath.Rel(photosDir, dstAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", "destination outside photos directory"
	}
	return dstRel, dstAbs, ""
}

// transferOp performs one move or copy whose source and destination have
// already been validated (source exists, destination free, parent dir
// created). It returns "" on success or the per-item error message.
type transferOp func(src, dstRel, srcAbs, dstAbs string) string

// handleTransfer is the shared implementation of /api/move and /api/copy:
// request parsing, access checks, per-item destination validation, and
// re-indexing the destination (carrying over known dimensions so it needs no
// decode). verb names the operation in logs; resultKey is the JSON key that
// lists the successful items.
func handleTransfer(w http.ResponseWriter, r *http.Request, verb, resultKey string, op transferOp) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Paths       []string `json:"paths"`
		Destination string   `json:"destination"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(body.Paths) == 0 {
		http.Error(w, "no paths", http.StatusBadRequest)
		return
	}
	if body.Destination != "" && !isValidPath(body.Destination) {
		http.Error(w, "invalid destination", http.StatusBadRequest)
		return
	}
	u := userFromCtx(r)
	if !userCanAccessFolder(u, body.Destination) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	for _, p := range body.Paths {
		if isValidPath(p) && !userCanAccessPath(u, p) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	type doneItem struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	done := []doneItem{}
	failed := []failedItem{}

	for _, src := range body.Paths {
		dstRel, dstAbs, errMsg := resolveMoveCopyDestination(src, body.Destination)
		if errMsg != "" {
			failed = append(failed, failedItem{Path: src, Error: errMsg})
			continue
		}
		if msg := transferOne(verb, src, dstRel, dstAbs, op); msg != "" {
			failed = append(failed, failedItem{Path: src, Error: msg})
			continue
		}
		done = append(done, doneItem{From: src, To: dstRel})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		resultKey: done,
		"failed":  failed,
	})
}

// transferOne runs op for one validated src → dstRel pair while holding the
// locks for both paths, so the destination-exists check and the operation
// are atomic with respect to uploads, crops and thumbnail writes. Returns ""
// on success or the per-item error message.
func transferOne(verb, src, dstRel, dstAbs string, op transferOp) string {
	unlock := lockFiles(src, dstRel)
	defer unlock()
	srcAbs := filepath.Join(photosDir, filepath.FromSlash(src))
	if _, err := os.Stat(srcAbs); err != nil {
		if os.IsNotExist(err) {
			return "not found"
		}
		return "stat failed"
	}
	if _, err := os.Stat(dstAbs); err == nil {
		return "destination exists"
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		log.Printf("%s: mkdir %s: %v", verb, filepath.Dir(dstAbs), err)
		return "mkdir failed"
	}
	var srcW, srcH int
	readDB().QueryRow(`SELECT width, height FROM files WHERE path = ?`, src).Scan(&srcW, &srcH) //nolint:errcheck
	if msg := op(src, dstRel, srcAbs, dstAbs); msg != "" {
		return msg
	}
	indexFileRecord(dstRel, srcW, srcH)
	log.Printf("%s: %s -> %s", verb, src, dstRel)
	return ""
}

func handleMove(w http.ResponseWriter, r *http.Request) {
	handleTransfer(w, r, "move", "moved", func(src, dstRel, srcAbs, dstAbs string) string {
		// Re-key favorites and view history before touching the file: the
		// watcher reacts to the source disappearing by deleting rows for the
		// old path, so they must already live under the new path by then.
		if err := moveUserData(src, dstRel); err != nil {
			log.Printf("move: re-key user data %s -> %s: %v", src, dstRel, err)
			return "move failed"
		}
		if err := moveFile(srcAbs, dstAbs); err != nil {
			log.Printf("move: %s -> %s: %v", srcAbs, dstAbs, err)
			if rerr := moveUserData(dstRel, src); rerr != nil {
				log.Printf("move: restore user data %s: %v", src, rerr)
			}
			return "move failed"
		}
		// Update mtime so the file shows up as freshly added at the destination
		// rather than at its original capture time.
		now := time.Now()
		os.Chtimes(dstAbs, now, now) //nolint:errcheck
		os.Remove(thumbSmallCachePath(src))
		os.Remove(thumbMediumCachePath(src))
		deleteFile(src)
		return ""
	})
}

func handleCopy(w http.ResponseWriter, r *http.Request) {
	handleTransfer(w, r, "copy", "copied", func(src, dstRel, srcAbs, dstAbs string) string {
		if err := copyFile(srcAbs, dstAbs); err != nil {
			log.Printf("copy: %s -> %s: %v", srcAbs, dstAbs, err)
			return "copy failed"
		}
		return ""
	})
}

// moveFile renames srcAbs to dstAbs. rename(2) can't span filesystems, so
// when the destination folder is on a different mount (e.g. bind mount) it
// falls back to copy + delete.
func moveFile(srcAbs, dstAbs string) error {
	err := os.Rename(srcAbs, dstAbs)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyFile(srcAbs, dstAbs); err != nil {
		return fmt.Errorf("cross-device copy: %w", err)
	}
	if err := os.Remove(srcAbs); err != nil {
		os.Remove(dstAbs)
		return fmt.Errorf("cross-device remove source: %w", err)
	}
	return nil
}

func copyFile(srcAbs, dstAbs string) error {
	s, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer s.Close()
	fi, err := s.Stat()
	if err != nil {
		return err
	}
	d, err := os.OpenFile(dstAbs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		os.Remove(dstAbs)
		return err
	}
	if err := d.Close(); err != nil {
		os.Remove(dstAbs)
		return err
	}
	// Leave mtime as the file-creation time (now) so the copy is treated as
	// freshly added rather than carrying the original capture time.
	return nil
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
	if !userCanAccessPath(userFromCtx(r), imgPath) {
		http.Error(w, "forbidden", http.StatusForbidden)
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

	mu := lockFile(imgPath)
	defer mu.Unlock()

	originalData, err := os.ReadFile(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "image not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to read image", http.StatusInternalServerError)
		}
		return
	}

	if cfg, _, err := image.DecodeConfig(bytes.NewReader(originalData)); err == nil {
		if err := checkPixelLimit(cfg.Width, cfg.Height); err != nil {
			http.Error(w, "image too large", http.StatusUnprocessableEntity)
			return
		}
	}
	if err := acquireDecode(r.Context()); err != nil {
		return
	}
	defer releaseDecode()

	img, err := imaging.Decode(bytes.NewReader(originalData), imaging.AutoOrientation(true))
	if err != nil {
		http.Error(w, "failed to decode image", http.StatusInternalServerError)
		return
	}

	bounds := img.Bounds()
	x0, y0 := body.X, body.Y
	x1, y1 := body.X+body.Width, body.Y+body.Height
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
	if err := writeFileAtomic(srcPath, outData, 0o644, true); err != nil {
		log.Printf("crop: write %s: %v", srcPath, err)
		http.Error(w, "failed to replace image", http.StatusInternalServerError)
		return
	}

	os.Remove(thumbSmallCachePath(imgPath))
	os.Remove(thumbMediumCachePath(imgPath))
	indexFileRecord(imgPath, b.Dx(), b.Dy())

	uid := ""
	if u := userFromCtx(r); u != nil {
		uid = u.ID
	}
	info, err := lookupFile(imgPath, uid)
	if err != nil {
		http.Error(w, "failed to load cropped record", http.StatusInternalServerError)
		return
	}

	log.Printf("crop: %s (%dx%d)", imgPath, b.Dx(), b.Dy())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info) //nolint:errcheck
}

func handleFavorite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	imgPath := strings.TrimPrefix(r.URL.Path, "/api/favorite/")
	if !isValidPath(imgPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	u := userFromCtx(r)
	if !userCanAccessPath(u, imgPath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Favorited bool `json:"favorited"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	uid := ""
	if u != nil {
		uid = u.ID
	}
	if err := setFavorite(imgPath, uid, body.Favorited); err != nil {
		log.Printf("favorite: %s (%s): %v", imgPath, uid, err)
		http.Error(w, "failed to update favorite", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleFolders(w http.ResponseWriter, r *http.Request) {
	direction := "ASC"
	if strings.EqualFold(r.URL.Query().Get("order"), "desc") {
		direction = "DESC"
	}
	rows, err := readDB().Query(`SELECT DISTINCT folder FROM files WHERE folder != '' ORDER BY folder ` + direction)
	if err != nil {
		log.Printf("handleFolders: query error: %v", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	u := userFromCtx(r)
	type folderEntry struct {
		Path       string   `json:"path"`
		Scope      string   `json:"scope"`
		SharedWith []string `json:"sharedWith,omitempty"`
	}
	folders := []folderEntry{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			log.Printf("handleFolders: scan error: %v", err)
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		if !userCanAccessFolder(u, f) {
			continue
		}
		top := f
		if i := strings.IndexByte(f, '/'); i >= 0 {
			top = f[:i]
		}
		scope, sharedWith := classifyTopFolder(top, u)
		folders = append(folders, folderEntry{Path: f, Scope: scope, SharedWith: sharedWith})
	}
	if err := rows.Err(); err != nil {
		log.Printf("handleFolders: rows error: %v", err)
		http.Error(w, "rows failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"folders": folders,
	})
}

// buildNumber is injected at build time via -ldflags "-X main.buildNumber=…".
// In dev runs (and tests) it stays as "dev".
var buildNumber = "dev"

func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	titleIcon := iconsDir != ""

	var imageCount int
	var imageTotalBytes int64
	if err := readDB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files`).Scan(&imageCount, &imageTotalBytes); err != nil {
		log.Printf("handleConfig: stats query: %v", err)
	}

	var diskFreeBytes int64
	var stat syscall.Statfs_t
	if err := syscall.Statfs(photosDir, &stat); err != nil {
		log.Printf("handleConfig: statfs %s: %v", photosDir, err)
	} else {
		diskFreeBytes = int64(stat.Bavail) * int64(stat.Bsize)
	}

	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"title":           appTitle,
		"videoEnabled":    videoEnabled,
		"bgColor":         bgColor,
		"titleIcon":       titleIcon,
		"buildNumber":     buildNumber,
		"imageCount":      imageCount,
		"imageTotalBytes": imageTotalBytes,
		"diskFreeBytes":   diskFreeBytes,
		"usersEnabled":    appConfig != nil,
	})
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	type publicUser struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	users := []publicUser{}
	if appConfig != nil {
		for _, u := range appConfig.Users {
			users = append(users, publicUser{ID: u.ID, Name: u.Name})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"users": users,
	})
}

// buildManifest takes the raw embedded manifest bytes, merges in runtime
// config (title, background colour, video accept types), and returns the
// JSON bytes to serve. Called once at startup; the result is cached in
// manifestBody.
func buildManifest(raw []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
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
	return json.Marshal(m)
}

// handleShareTargetFallback answers the manifest's share_target POST when the
// service worker didn't intercept it (SW evicted, not yet installed, …). The
// shared files are discarded; the share-error=no-sw flag lets the app tell
// the user why, instead of silently serving the SPA at /share-target.
func handleShareTargetFallback(w http.ResponseWriter, r *http.Request) {
	log.Printf("share-target: request reached server (service worker bypassed)")
	http.Redirect(w, r, "/?share-pending=1&share-error=no-sw", http.StatusSeeOther)
}

func handleManifest(w http.ResponseWriter, r *http.Request) {
	if manifestBody == nil {
		http.Error(w, "manifest not available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(manifestBody) //nolint:errcheck
}

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

func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case p == "" || p == "index.html" || p == "sw.js":
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(p, "assets/"):
			// Vite emits content-hashed filenames under assets/; safe to cache forever.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		f, err := fsys.Open(p)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		r2 := *r
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, &r2)
	})
}
