package main

import (
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
		{"IMG_20190318_132033.jpg", false, ""}, // prefix before date
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
