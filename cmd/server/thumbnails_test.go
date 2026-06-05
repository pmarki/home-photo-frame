package main

import (
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
