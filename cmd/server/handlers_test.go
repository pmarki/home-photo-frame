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

// setupTestEnv saves all mutable globals, wires up isolated temp dirs, and
// registers t.Cleanup to restore original state. Call at the top of every test.
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

	// Snapshot globals.
	oldPhotos, oldCache := photosDir, cacheDir
	oldTitle, oldVideo, oldBG, oldIcons, oldMedium := appTitle, videoEnabled, bgColor, iconsDir, mediumWidth

	imagesCacheMu.Lock()
	oldImages, oldSet, oldSorted := imagesCache, imageCacheSet, sortedCache
	imagesCacheMu.Unlock()

	metaMu.Lock()
	oldMeta, oldMetaPath := metaIndex, metaPath
	metaMu.Unlock()

	// Install clean state.
	photosDir = photos
	cacheDir = cache
	appTitle = "Test Frame"
	videoEnabled = false
	bgColor = "#000000"
	iconsDir = ""
	mediumWidth = 2000

	imagesCacheMu.Lock()
	imagesCache = nil
	imageCacheSet = nil
	sortedCache = nil
	imagesCacheMu.Unlock()

	metaMu.Lock()
	metaIndex = map[string]ImageMeta{}
	metaPath = filepath.Join(cache, "meta.json")
	metaMu.Unlock()

	t.Cleanup(func() {
		photosDir = oldPhotos
		cacheDir = oldCache
		appTitle = oldTitle
		videoEnabled = oldVideo
		bgColor = oldBG
		iconsDir = oldIcons
		mediumWidth = oldMedium

		imagesCacheMu.Lock()
		imagesCache = oldImages
		imageCacheSet = oldSet
		sortedCache = oldSorted
		imagesCacheMu.Unlock()

		metaMu.Lock()
		metaIndex = oldMeta
		metaPath = oldMetaPath
		metaMu.Unlock()
	})
}

// makeTestJPEG returns a valid decodable JPEG of size w×h.
func makeTestJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}) //nolint:errcheck
	return buf.Bytes()
}

// seedFile writes data to photosDir/name, adds it to imagesCache, and returns
// the resulting ImageInfo.
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
	small, medium, original := thumbURLs(name)
	info := ImageInfo{
		Filename:    name,
		Path:        name,
		ModTime:     fi.ModTime(),
		FileMtime:   fi.ModTime(),
		Size:        fi.Size(),
		ThumbSmall:  small,
		ThumbMedium: medium,
		Original:    original,
	}
	cacheAdd(info)
	return info
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

// ── TestHandleImages ──────────────────────────────────────────────────────

func TestHandleImages(t *testing.T) {
	t.Run("empty cache returns zero total", func(t *testing.T) {
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
		now := time.Now()
		for _, name := range []string{"c.jpg", "a.jpg", "b.jpg"} {
			cacheAdd(ImageInfo{Filename: name, Path: name, ModTime: now})
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
		now := time.Now()
		for _, name := range []string{"c.jpg", "a.jpg", "b.jpg"} {
			cacheAdd(ImageInfo{Filename: name, Path: name, ModTime: now})
		}
		rr := doRequest(handleImages, http.MethodGet, "/api/images?sort=name&order=desc", nil, "")
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if len(resp.Images) == 0 || resp.Images[0].Path != "c.jpg" {
			t.Errorf("images[0].path = %q, want c.jpg", resp.Images[0].Path)
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
		// Submit a multipart form with no "file" field.
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
		imagesCacheMu.RLock()
		_, inSet := imageCacheSet["photo.jpg"]
		imagesCacheMu.RUnlock()
		if inSet {
			t.Error("photo.jpg still in imageCacheSet after delete")
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
