package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Helpers ───────────────────────────────────────────────────────────────

// setupTestEnv saves all mutable globals, wires up isolated temp dirs and an
// in-memory SQLite database, and registers t.Cleanup to restore original state.
// Call at the top of every test.
func setupTestEnv(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	photos := filepath.Join(tmp, "photos")
	cache := filepath.Join(tmp, "cache")
	for _, d := range []string{photos, filepath.Join(cache, "s"), filepath.Join(cache, "m")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("setupTestEnv mkdir: %v", err)
		}
	}

	oldPhotos, oldCache := photosDir, cacheDir
	oldTitle, oldVideo, oldBG, oldIcons, oldMedium := appTitle, videoEnabled, bgColor, iconsDir, mediumWidth
	oldDB := db

	photosDir = photos
	cacheDir = cache
	appTitle = "Test Frame"
	videoEnabled = false
	bgColor = "#000000"
	iconsDir = ""
	mediumWidth = 2000

	testDB, err := openDB(":memory:")
	if err != nil {
		t.Fatalf("setupTestEnv openDB: %v", err)
	}
	db = testDB

	t.Cleanup(func() {
		testDB.Close()
		photosDir = oldPhotos
		cacheDir = oldCache
		appTitle = oldTitle
		videoEnabled = oldVideo
		bgColor = oldBG
		iconsDir = oldIcons
		mediumWidth = oldMedium
		db = oldDB
	})
}

// makeTestJPEG returns a valid decodable JPEG of size w×h.
func makeTestJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}) //nolint:errcheck
	return buf.Bytes()
}

// seedFile writes data to photosDir/name, inserts it into the test database,
// and returns the resulting ImageInfo.
func seedFile(t *testing.T, name string, data []byte) ImageInfo {
	t.Helper()
	dst := filepath.Join(photosDir, name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("seedFile write: %v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("seedFile stat: %v", err)
	}
	now := time.Now().UnixNano()
	if _, err := db.Exec(
		`INSERT OR REPLACE INTO files
		 (path, filename, folder, file_type, width, height, size, file_mtime, date_taken, indexed_at)
		 VALUES (?, ?, '', 'image', 0, 0, ?, ?, ?, ?)`,
		name, name, fi.Size(), fi.ModTime().UnixNano(), fi.ModTime().UnixNano(), now,
	); err != nil {
		t.Fatalf("seedFile db insert: %v", err)
	}
	small, medium, original := thumbURLs(name, fi.ModTime())
	return ImageInfo{
		Filename:    name,
		Path:        name,
		ModTime:     fi.ModTime(),
		FileMtime:   fi.ModTime(),
		Size:        fi.Size(),
		ThumbSmall:  small,
		ThumbMedium: medium,
		Original:    original,
	}
}

// doRequest fires a request directly at handler and returns the recorder.
func doRequest(handler http.HandlerFunc, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	handler(rr, req)
	return rr
}

// multipartFile builds a multipart/form-data body with a single "file" field.
func multipartFile(t *testing.T, filename string, data []byte) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write(data) //nolint:errcheck
	w.Close()
	return body, w.FormDataContentType()
}

// ── TestHandleConfig ──────────────────────────────────────────────────────

func TestHandleConfig(t *testing.T) {
	t.Run("returns correct fields", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleConfig, http.MethodGet, "/api/config", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var m map[string]any
		if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if m["title"] != "Test Frame" {
			t.Errorf("title = %q, want %q", m["title"], "Test Frame")
		}
		if m["bgColor"] != "#000000" {
			t.Errorf("bgColor = %q, want %q", m["bgColor"], "#000000")
		}
		if m["videoEnabled"] != false {
			t.Errorf("videoEnabled = %v, want false", m["videoEnabled"])
		}
		if m["titleIcon"] != false {
			t.Errorf("titleIcon = %v, want false (no iconsDir set)", m["titleIcon"])
		}
		if m["buildNumber"] != "dev" {
			t.Errorf("buildNumber = %q, want %q", m["buildNumber"], "dev")
		}
		if m["imageCount"] != float64(0) {
			t.Errorf("imageCount = %v, want 0", m["imageCount"])
		}
		if m["imageTotalBytes"] != float64(0) {
			t.Errorf("imageTotalBytes = %v, want 0", m["imageTotalBytes"])
		}
		if free, ok := m["diskFreeBytes"].(float64); !ok || free <= 0 {
			t.Errorf("diskFreeBytes = %v, want positive number", m["diskFreeBytes"])
		}
	})

	t.Run("counts seeded files and sums their sizes", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		seedFile(t, "b.jpg", makeTestJPEG(20, 20))
		rr := doRequest(handleConfig, http.MethodGet, "/api/config", nil, "")
		var m map[string]any
		json.NewDecoder(rr.Body).Decode(&m) //nolint:errcheck
		if m["imageCount"] != float64(2) {
			t.Errorf("imageCount = %v, want 2", m["imageCount"])
		}
		if total, ok := m["imageTotalBytes"].(float64); !ok || total <= 0 {
			t.Errorf("imageTotalBytes = %v, want positive sum", m["imageTotalBytes"])
		}
	})

	t.Run("titleIcon true when iconsDir is set", func(t *testing.T) {
		setupTestEnv(t)
		iconsDir = "/some/icons"

		rr := doRequest(handleConfig, http.MethodGet, "/api/config", nil, "")
		var m map[string]any
		json.NewDecoder(rr.Body).Decode(&m) //nolint:errcheck
		if m["titleIcon"] != true {
			t.Errorf("titleIcon = %v, want true", m["titleIcon"])
		}
	})
}

// ── TestHandleFolders ─────────────────────────────────────────────────────

func TestHandleFolders(t *testing.T) {
	t.Run("empty database returns empty list", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleFolders, http.MethodGet, "/api/folders", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var resp struct{ Folders []string }
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Folders) != 0 {
			t.Errorf("folders = %v, want empty", resp.Folders)
		}
	})

	t.Run("returns sorted distinct non-empty folders", func(t *testing.T) {
		setupTestEnv(t)
		now := time.Now().UnixNano()
		rows := []struct{ path, folder string }{
			{"vacation/hawaii/a.jpg", "vacation/hawaii"},
			{"vacation/hawaii/b.jpg", "vacation/hawaii"}, // dup folder
			{"vacation/c.jpg", "vacation"},
			{"family/d.jpg", "family"},
			{"root.jpg", ""}, // root file, should be excluded
		}
		for _, r := range rows {
			if _, err := db.Exec(
				`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES (?,?,?,'image',0,0,0,?,?,?)`,
				r.path, filepath.Base(r.path), r.folder, now, now, now,
			); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
		rr := doRequest(handleFolders, http.MethodGet, "/api/folders", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var resp struct{ Folders []string }
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		want := []string{"family", "vacation", "vacation/hawaii"}
		if len(resp.Folders) != len(want) {
			t.Fatalf("folders = %v, want %v", resp.Folders, want)
		}
		for i, w := range want {
			if resp.Folders[i] != w {
				t.Errorf("folders[%d] = %q, want %q", i, resp.Folders[i], w)
			}
		}
	})
}

// ── TestHandleImages ──────────────────────────────────────────────────────

func TestHandleImages(t *testing.T) {
	t.Run("empty database returns zero total", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleImages, http.MethodGet, "/api/images", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if resp.Total != 0 || len(resp.Images) != 0 {
			t.Errorf("want empty, got total=%d images=%d", resp.Total, len(resp.Images))
		}
	})

	t.Run("returns all seeded images", func(t *testing.T) {
		setupTestEnv(t)
		for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
			seedFile(t, name, makeTestJPEG(10, 10))
		}
		rr := doRequest(handleImages, http.MethodGet, "/api/images", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if resp.Total != 3 {
			t.Errorf("total = %d, want 3", resp.Total)
		}
		if len(resp.Images) != 3 {
			t.Errorf("len(images) = %d, want 3", len(resp.Images))
		}
	})

	t.Run("pagination returns correct page", func(t *testing.T) {
		setupTestEnv(t)
		for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
			seedFile(t, name, makeTestJPEG(10, 10))
		}
		rr := doRequest(handleImages, http.MethodGet, "/api/images?sort=name&order=asc&limit=2&page=1", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if resp.Total != 3 {
			t.Errorf("total = %d, want 3", resp.Total)
		}
		if len(resp.Images) != 2 {
			t.Errorf("len(images) = %d, want 2", len(resp.Images))
		}
	})

	t.Run("sort name asc", func(t *testing.T) {
		setupTestEnv(t)
		now := time.Now().UnixNano()
		for _, name := range []string{"c.jpg", "a.jpg", "b.jpg"} {
			db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES (?,?,'','image',0,0,0,?,?,?)`, //nolint:errcheck
				name, name, now, now, now)
		}
		rr := doRequest(handleImages, http.MethodGet, "/api/images?sort=name&order=asc", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if len(resp.Images) == 0 || resp.Images[0].Path != "a.jpg" {
			t.Errorf("images[0].path = %q, want a.jpg", resp.Images[0].Path)
		}
	})

	t.Run("sort name desc", func(t *testing.T) {
		setupTestEnv(t)
		now := time.Now().UnixNano()
		for _, name := range []string{"c.jpg", "a.jpg", "b.jpg"} {
			db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES (?,?,'','image',0,0,0,?,?,?)`, //nolint:errcheck
				name, name, now, now, now)
		}
		rr := doRequest(handleImages, http.MethodGet, "/api/images?sort=name&order=desc", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if len(resp.Images) == 0 || resp.Images[0].Path != "c.jpg" {
			t.Errorf("images[0].path = %q, want c.jpg", resp.Images[0].Path)
		}
	})

	t.Run("filter by folder", func(t *testing.T) {
		setupTestEnv(t)
		now := time.Now().UnixNano()
		rows := []struct{ path, folder string }{
			{"vacation/a.jpg", "vacation"},
			{"vacation/hawaii/b.jpg", "vacation/hawaii"},
			{"c.jpg", ""},
		}
		for _, r := range rows {
			db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES (?,?,?,'image',0,0,0,?,?,?)`, //nolint:errcheck
				r.path, filepath.Base(r.path), r.folder, now, now, now)
		}
		rr := doRequest(handleImages, http.MethodGet, "/api/images?folder=vacation", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if resp.Total != 2 {
			t.Errorf("total = %d, want 2 (vacation + subfolder)", resp.Total)
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		setupTestEnv(t)
		now := time.Now().UnixNano()
		db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES ('a.jpg','a.jpg','','image',0,0,0,?,?,?)`, now, now, now)   //nolint:errcheck
		db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES ('b.mp4','b.mp4','','video',0,0,0,?,?,?)`, now, now, now)   //nolint:errcheck
		rr := doRequest(handleImages, http.MethodGet, "/api/images?type=video", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if resp.Total != 1 || resp.Images[0].Path != "b.mp4" {
			t.Errorf("type filter: got total=%d path=%q, want 1 b.mp4", resp.Total, resp.Images[0].Path)
		}
	})

	t.Run("search by filename", func(t *testing.T) {
		setupTestEnv(t)
		now := time.Now().UnixNano()
		db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES ('beach_2024.jpg','beach_2024.jpg','','image',0,0,0,?,?,?)`, now, now, now) //nolint:errcheck
		db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES ('mountain.jpg','mountain.jpg','','image',0,0,0,?,?,?)`, now, now, now)     //nolint:errcheck
		rr := doRequest(handleImages, http.MethodGet, "/api/images?search=beach", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if resp.Total != 1 || resp.Images[0].Path != "beach_2024.jpg" {
			t.Errorf("search: got total=%d, want 1 beach_2024.jpg", resp.Total)
		}
	})
}

// ── TestHandleUpload ──────────────────────────────────────────────────────

func TestHandleUpload(t *testing.T) {
	t.Run("valid JPEG upload succeeds", func(t *testing.T) {
		setupTestEnv(t)
		body, ct := multipartFile(t, "photo.jpg", makeTestJPEG(20, 20))
		rr := doRequest(handleUpload, http.MethodPost, "/api/upload", body, ct)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
		}
		var m map[string]string
		json.NewDecoder(rr.Body).Decode(&m) //nolint:errcheck
		if m["filename"] != "photo.jpg" {
			t.Errorf("filename = %q, want photo.jpg", m["filename"])
		}
		if _, err := os.Stat(filepath.Join(photosDir, "photo.jpg")); err != nil {
			t.Errorf("uploaded file not found on disk: %v", err)
		}
	})

	t.Run("unsupported extension returns 422", func(t *testing.T) {
		setupTestEnv(t)
		body, ct := multipartFile(t, "malware.exe", []byte("data"))
		rr := doRequest(handleUpload, http.MethodPost, "/api/upload", body, ct)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", rr.Code)
		}
	})

	t.Run("missing file field returns 400", func(t *testing.T) {
		setupTestEnv(t)
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		w.WriteField("other", "value") //nolint:errcheck
		w.Close()
		rr := doRequest(handleUpload, http.MethodPost, "/api/upload", &body, w.FormDataContentType())
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleUpload, http.MethodGet, "/api/upload", nil, "")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rr.Code)
		}
	})

	t.Run("duplicate file returns 409", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "photo.jpg", makeTestJPEG(10, 10))
		body, ct := multipartFile(t, "photo.jpg", makeTestJPEG(10, 10))
		rr := doRequest(handleUpload, http.MethodPost, "/api/upload", body, ct)
		if rr.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409", rr.Code)
		}
	})
}

// ── TestHandleDelete ──────────────────────────────────────────────────────

func TestHandleDelete(t *testing.T) {
	t.Run("deletes existing file", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "photo.jpg", makeTestJPEG(10, 10))

		rr := doRequest(handleDelete, http.MethodDelete, "/api/delete/photo.jpg", nil, "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body: %s", rr.Code, rr.Body.String())
		}
		if _, err := os.Stat(filepath.Join(photosDir, "photo.jpg")); !os.IsNotExist(err) {
			t.Error("file still exists on disk after delete")
		}
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM files WHERE path = 'photo.jpg'`).Scan(&count) //nolint:errcheck
		if count != 0 {
			t.Error("photo.jpg still in database after delete")
		}
	})

	t.Run("non-existent file returns 404", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleDelete, http.MethodDelete, "/api/delete/ghost.jpg", nil, "")
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("path traversal returns 400", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleDelete, http.MethodDelete, "/api/delete/../etc/passwd", nil, "")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleDelete, http.MethodGet, "/api/delete/photo.jpg", nil, "")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rr.Code)
		}
	})
}

// ── TestHandleCrop ────────────────────────────────────────────────────────

func TestHandleCrop(t *testing.T) {
	cropBody := func(x, y, w, h int) *strings.Reader {
		b, _ := json.Marshal(map[string]int{"x": x, "y": y, "width": w, "height": h})
		return strings.NewReader(string(b))
	}

	t.Run("valid crop updates dimensions", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "photo.jpg", makeTestJPEG(20, 20))

		rr := doRequest(handleCrop, http.MethodPost, "/api/crop/photo.jpg",
			cropBody(0, 0, 10, 8), "application/json")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
		}
		var m map[string]any
		json.NewDecoder(rr.Body).Decode(&m) //nolint:errcheck
		if m["width"] != float64(10) || m["height"] != float64(8) {
			t.Errorf("dimensions = %v×%v, want 10×8", m["width"], m["height"])
		}
	})

	t.Run("crop rect entirely outside image returns 400", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "photo.jpg", makeTestJPEG(10, 10))

		rr := doRequest(handleCrop, http.MethodPost, "/api/crop/photo.jpg",
			cropBody(20, 20, 5, 5), "application/json")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("zero dimensions returns 400", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "photo.jpg", makeTestJPEG(10, 10))

		rr := doRequest(handleCrop, http.MethodPost, "/api/crop/photo.jpg",
			cropBody(0, 0, 0, 0), "application/json")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("invalid path returns 400", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleCrop, http.MethodPost, "/api/crop/../etc/passwd",
			cropBody(0, 0, 5, 5), "application/json")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleCrop, http.MethodGet, "/api/crop/photo.jpg", nil, "")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rr.Code)
		}
	})
}
