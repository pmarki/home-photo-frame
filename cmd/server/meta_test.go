package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilenameDateRegex(t *testing.T) {
	tests := []struct {
		input   string
		matches bool
		year    string
	}{
		{"20190318_132033", true, "2019"},
		{"20190318_132033_extra", true, "2019"},
		{"20240101_000000.jpg", true, "2024"},
		{"photo_20190318_132033", false, ""},  // not at start
		{"20190318", false, ""},               // missing time
		{"20190318_1320", false, ""},          // time too short
		{"2019031813", false, ""},             // no underscore separator
		{"IMG_20190318_132033.jpg", false, ""}, // prefix before date — handled by filenameDatePrefixed
	}
	for _, tc := range tests {
		m := filenameDate.FindStringSubmatch(tc.input)
		if tc.matches {
			if m == nil {
				t.Errorf("filenameDate.FindStringSubmatch(%q) = nil, want match", tc.input)
				continue
			}
			if m[1] != tc.year {
				t.Errorf("filenameDate.FindStringSubmatch(%q) year = %q, want %q", tc.input, m[1], tc.year)
			}
		} else {
			if m != nil {
				t.Errorf("filenameDate.FindStringSubmatch(%q) = %v, want nil", tc.input, m)
			}
		}
	}
}

func TestFilenameDateParsing(t *testing.T) {
	// Verify the concatenated groups produce a parseable timestamp.
	tests := []struct {
		filename string
		wantYear int
		wantMon  time.Month
		wantDay  int
	}{
		{"20190318_132033_photo.jpg", 2019, time.March, 18},
		{"20240714_083045.jpg", 2024, time.July, 14},
	}
	for _, tc := range tests {
		base := tc.filename
		m := filenameDate.FindStringSubmatch(base)
		if m == nil {
			t.Fatalf("no match for %q", base)
		}
		s := m[1] + m[2] + m[3] + m[4] + m[5] + m[6]
		got, err := time.ParseInLocation("20060102150405", s, time.UTC)
		if err != nil {
			t.Errorf("parse %q: %v", s, err)
			continue
		}
		if got.Year() != tc.wantYear || got.Month() != tc.wantMon || got.Day() != tc.wantDay {
			t.Errorf("parsed %q = %v, want %d-%02d-%02d", s, got, tc.wantYear, tc.wantMon, tc.wantDay)
		}
	}
}

func TestFilenameDatePrefixedRegex(t *testing.T) {
	tests := []struct {
		input   string
		matches bool
		year    string
	}{
		{"IMG_20151008_115901371", true, "2015"},  // Pixel-style trailing ms
		{"IMG_20151008_115901371.jpg", true, "2015"},
		{"IMG_20190318_132033.jpg", true, "2019"},
		{"VID_20190318_132033.mp4", true, "2019"},
		{"PXL_20231115_180004.jpg", true, "2023"},
		{"Screenshot_20240101_000000.png", true, "2024"},
		{"20190318_132033.jpg", false, ""}, // no letter prefix
		{"IMG20190318_132033.jpg", false, ""}, // missing underscore after prefix
		{"IMG_2019031_132033.jpg", false, ""}, // date too short
		{"IMG_20190318132033.jpg", false, ""}, // no underscore between date and time
		{"_20190318_132033.jpg", false, ""},   // empty prefix
	}
	for _, tc := range tests {
		m := filenameDatePrefixed.FindStringSubmatch(tc.input)
		if tc.matches {
			if m == nil {
				t.Errorf("filenameDatePrefixed.FindStringSubmatch(%q) = nil, want match", tc.input)
				continue
			}
			if m[1] != tc.year {
				t.Errorf("filenameDatePrefixed.FindStringSubmatch(%q) year = %q, want %q", tc.input, m[1], tc.year)
			}
		} else {
			if m != nil {
				t.Errorf("filenameDatePrefixed.FindStringSubmatch(%q) = %v, want nil", tc.input, m)
			}
		}
	}
}

func TestExtractBestDateFromPrefixedFilename(t *testing.T) {
	setupTestEnv(t)
	// File with no EXIF; extractBestDate should fall back to the filename pattern.
	name := "IMG_20151008_115901371.jpg"
	dst := filepath.Join(photosDir, name)
	if err := os.WriteFile(dst, makeTestJPEG(10, 10), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := extractBestDate(name, dst)
	if got.Year() != 2015 || got.Month() != time.October || got.Day() != 8 {
		t.Errorf("extractBestDate(%q) = %v, want 2015-10-08", name, got)
	}
	if got.Hour() != 11 || got.Minute() != 59 || got.Second() != 1 {
		t.Errorf("extractBestDate(%q) time = %02d:%02d:%02d, want 11:59:01",
			name, got.Hour(), got.Minute(), got.Second())
	}
}
