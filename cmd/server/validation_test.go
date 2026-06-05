package main

import "testing"

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
