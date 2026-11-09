package main

import (
	"strconv"
	"sync"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThumbHash(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("deterministic", func(t *testing.T) {
		a := thumbHash("photo.jpg", t0)
		b := thumbHash("photo.jpg", t0)
		if a != b {
			t.Errorf("same inputs produced different hashes: %q vs %q", a, b)
		}
	})

	t.Run("length", func(t *testing.T) {
		h := thumbHash("photo.jpg", t0)
		if len(h) != 16 {
			t.Errorf("hash length = %d, want 16", len(h))
		}
	})

	t.Run("hex only", func(t *testing.T) {
		h := thumbHash("photo.jpg", t0)
		for _, c := range h {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("hash %q contains non-hex character %q", h, c)
				break
			}
		}
	})

	t.Run("different filename", func(t *testing.T) {
		if thumbHash("a.jpg", t0) == thumbHash("b.jpg", t0) {
			t.Error("different filenames produced the same hash")
		}
	})

	t.Run("different mtime", func(t *testing.T) {
		t1 := t0.Add(time.Second)
		if thumbHash("photo.jpg", t0) == thumbHash("photo.jpg", t1) {
			t.Error("different mtimes produced the same hash")
		}
	})
}

func TestThumbCachePaths(t *testing.T) {
	old := cacheDir
	cacheDir = "/cache"
	t.Cleanup(func() { cacheDir = old })

	tests := []struct {
		fn       func(string) string
		input    string
		wantSub  string // expected path component after /cache/
	}{
		{thumbSmallCachePath, "photo.jpg", filepath.Join("s", "photo.jpg")},
		{thumbSmallCachePath, "vacation/photo.jpg", filepath.Join("s", "vacation", "photo.jpg")},
		{thumbMediumCachePath, "photo.jpg", filepath.Join("m", "photo.jpg")},
		{thumbMediumCachePath, "vacation/photo.jpg", filepath.Join("m", "vacation", "photo.jpg")},
	}
	for _, tc := range tests {
		got := tc.fn(tc.input)
		want := filepath.Join("/cache", tc.wantSub)
		if got != want {
			t.Errorf("fn(%q) = %q, want %q", tc.input, got, want)
		}
	}
}

func TestThumbPixelLimit(t *testing.T) {
	setupTestEnv(t)
	old := maxImagePixels
	maxImagePixels = 100
	t.Cleanup(func() { maxImagePixels = old })

	info := seedFile(t, "big.jpg", makeTestJPEG(20, 20)) // 400 px > 100
	rr := doRequest(handleThumb, http.MethodGet, info.ThumbSmall, nil, "")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	if _, err := os.Stat(thumbSmallCachePath("big.jpg")); err == nil {
		t.Error("thumbnail was cached for an over-limit image")
	}
}

func TestThumbRegeneratesEmptyCacheFile(t *testing.T) {
	setupTestEnv(t)
	info := seedFile(t, "p.jpg", makeTestJPEG(10, 10))
	cp := thumbSmallCachePath("p.jpg")
	if err := os.WriteFile(cp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rr := doRequest(handleThumb, http.MethodGet, info.ThumbSmall, nil, "")
	if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
		t.Fatalf("status = %d, body len %d; want 200 with data", rr.Code, rr.Body.Len())
	}
	if fi, err := os.Stat(cp); err != nil || fi.Size() == 0 {
		t.Error("empty cache file was not regenerated")
	}
}

func TestThumbGeneratesBothSizesFromOneRequest(t *testing.T) {
	setupTestEnv(t)
	info := seedFile(t, "p.jpg", makeTestJPEG(30, 20))
	rr := doRequest(handleThumb, http.MethodGet, info.ThumbSmall, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !cacheFileReady(thumbMediumCachePath("p.jpg")) {
		t.Error("medium thumb was not generated alongside the small one")
	}
}

func TestThumbRejectsNonMediaPath(t *testing.T) {
	setupTestEnv(t)
	os.WriteFile(filepath.Join(photosDir, "notes.txt"), []byte("hi"), 0o644) //nolint:errcheck
	rr := doRequest(handleThumb, http.MethodGet, "/api/thumb/abcd/notes.txt", nil, "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// sameStripePair returns two distinct paths that hash to the same lock stripe.
func sameStripePair(t *testing.T) (string, string) {
	t.Helper()
	seen := map[int]string{}
	for i := 0; i < 10000; i++ {
		p := "p" + strconv.Itoa(i) + ".jpg"
		s := fileStripe(p)
		if q, ok := seen[s]; ok {
			return q, p
		}
		seen[s] = p
	}
	t.Fatal("no stripe collision found")
	return "", ""
}

func withTimeout(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: deadlocked", what)
	}
}

func TestLockFilesSameStripeNoSelfDeadlock(t *testing.T) {
	a, b := sameStripePair(t)
	withTimeout(t, "same stripe", func() { lockFiles(a, b)() })
	withTimeout(t, "same path twice", func() { lockFiles(a, a)() })
}

func TestLockFilesOppositeOrderNoDeadlock(t *testing.T) {
	a, b := "x/one.jpg", "y/two.jpg"
	withTimeout(t, "opposite order", func() {
		var wg sync.WaitGroup
		for i := 0; i < 200; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); lockFiles(a, b)() }()
			go func() { defer wg.Done(); lockFiles(b, a)() }()
		}
		wg.Wait()
	})
}

func TestLockFileExcludesSamePath(t *testing.T) {
	mu := lockFile("same.jpg")
	acquired := make(chan struct{})
	go func() {
		m := lockFile("same.jpg")
		close(acquired)
		m.Unlock()
	}()
	select {
	case <-acquired:
		t.Fatal("second lockFile on the same path did not block")
	case <-time.After(50 * time.Millisecond):
	}
	mu.Unlock()
	<-acquired
}
