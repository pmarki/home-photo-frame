package main

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsValidFilename(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"photo.jpg", true},
		{"my photo.jpg", true},
		{"IMG_20240101_120000.jpg", true},
		{"", false},
		{"..", false},
		{".", false},
		{"vacation/photo.jpg", false},
		{`photo\name.jpg`, false},
	}
	for _, tc := range tests {
		if got := isValidFilename(tc.input); got != tc.want {
			t.Errorf("isValidFilename(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestIsValidPath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"photo.jpg", true},
		{"vacation/photo.jpg", true},
		{"a/b/c.jpg", true},
		{"", false},
		{".", false},
		{"..", false},
		{"/photo.jpg", false},         // absolute path
		{"photo.jpg/", false},         // trailing slash
		{"../secret", false},          // traversal via component
		{"a/../b.jpg", false},         // un-cleaned path
		{"vacation//photo.jpg", false}, // double slash
	}
	for _, tc := range tests {
		if got := isValidPath(tc.input); got != tc.want {
			t.Errorf("isValidPath(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestIsValidHexColor(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"#0a0a0f", true},
		{"#FFFFFF", true},
		{"#000000", true},
		{"#0A0B0C", true},
		{"#abcdef", true},
		{"#gggggg", false}, // invalid hex digit
		{"#12345", false},  // too short
		{"#1234567", false}, // too long
		{"0a0a0f", false},  // missing #
		{"", false},
		{"#", false},
	}
	for _, tc := range tests {
		if got := isValidHexColor(tc.input); got != tc.want {
			t.Errorf("isValidHexColor(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestEncodePathSegments(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"photo.jpg", "photo.jpg"},
		{"vacation/photo.jpg", "vacation/photo.jpg"},
		{"my album/photo.jpg", "my%20album/photo.jpg"},
		{"jesień/photo.jpg", "jesie%C5%84/photo.jpg"},
		{"a/b/c.jpg", "a/b/c.jpg"},
		{"100% sun/shot.jpg", "100%25%20sun/shot.jpg"},
	}
	for _, tc := range tests {
		if got := encodePathSegments(tc.input); got != tc.want {
			t.Errorf("encodePathSegments(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestIsVideo(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"video.mp4", true},
		{"video.MP4", true},
		{"video.webm", true},
		{"video.mov", true},
		{"video.m4v", true},
		{"photo.jpg", false},
		{"photo.jpeg", false},
		{"photo.png", false},
		{"video.avi", false},
		{"video.mkv", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := isVideo(tc.input); got != tc.want {
			t.Errorf("isVideo(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestRepairOriginalJPEGRefusesNonJPEG(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"pic.png", "pic.gif", "pic.webp"} {
		p := filepath.Join(dir, name)
		orig := []byte("original bytes")
		if err := os.WriteFile(p, orig, 0o644); err != nil {
			t.Fatal(err)
		}
		err := repairOriginalJPEG(p, image.NewRGBA(image.Rect(0, 0, 4, 4)))
		if !errors.Is(err, errNotJPEG) {
			t.Errorf("%s: err = %v, want errNotJPEG", name, err)
		}
		if got, _ := os.ReadFile(p); string(got) != string(orig) {
			t.Errorf("%s: original was modified", name)
		}
	}
}

func TestRepairOriginalJPEGRewritesJPEG(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pic.JPG")
	if err := os.WriteFile(p, []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repairOriginalJPEG(p, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatalf("repair: %v", err)
	}
	got, _ := os.ReadFile(p)
	if len(got) < 2 || got[0] != 0xFF || got[1] != 0xD8 {
		t.Errorf("repaired file is not a JPEG")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.jpg")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("new"), 0o644, true); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("perm = %v, want 0644", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %d entries in dir", len(entries))
	}
}

func TestWriteFileAtomicMissingDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing", "out.jpg")
	if err := writeFileAtomic(p, []byte("x"), 0o644, false); err == nil {
		t.Error("expected error for missing directory")
	}
}

func TestSweepTempFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "album")
	os.MkdirAll(sub, 0o755) //nolint:errcheck
	old := time.Now().Add(-2 * tempFileMaxAge)
	write := func(name string, aged bool) string {
		p := filepath.Join(sub, name)
		os.WriteFile(p, []byte("x"), 0o644) //nolint:errcheck
		if aged {
			os.Chtimes(p, old, old) //nolint:errcheck
		}
		return p
	}
	gone := []string{
		write(".pf-123.tmp", true),   // writeFileAtomic orphan
		write("photo.jpg.tmp", true), // legacy crop/repair orphan
	}
	kept := []string{
		write(".pf-456.tmp", false), // possibly a write in progress
		write("notes.tmp", true),    // not ours
		write("photo.jpg", true),    // a real photo
	}

	if n := sweepTempFiles(dir); n != len(gone) {
		t.Errorf("removed %d, want %d", n, len(gone))
	}
	for _, p := range gone {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should have been removed", filepath.Base(p))
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s should have been kept", filepath.Base(p))
		}
	}
}
