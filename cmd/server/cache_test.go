package main

import (
	"testing"
	"time"
)

func TestSortIndices(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	images := []ImageInfo{
		// c.jpg: newest by taken date, middle by mtime
		{Path: "c.jpg", ModTime: now.Add(-1 * time.Hour), FileMtime: now.Add(-2 * time.Hour)},
		// a.jpg: oldest by taken date, newest by mtime
		{Path: "a.jpg", ModTime: now.Add(-3 * time.Hour), FileMtime: now.Add(-1 * time.Hour)},
		// b.jpg: middle by taken date, oldest by mtime
		{Path: "b.jpg", ModTime: now.Add(-2 * time.Hour), FileMtime: now.Add(-3 * time.Hour)},
	}
	indices := func() []int { return []int{0, 1, 2} }

	tests := []struct {
		by    string
		order string
		want  []string
	}{
		{"name", "asc", []string{"a.jpg", "b.jpg", "c.jpg"}},
		{"name", "desc", []string{"c.jpg", "b.jpg", "a.jpg"}},
		{"date", "asc", []string{"a.jpg", "b.jpg", "c.jpg"}},
		{"date", "desc", []string{"c.jpg", "b.jpg", "a.jpg"}},
		{"taken", "asc", []string{"a.jpg", "b.jpg", "c.jpg"}}, // alias for date
		{"taken", "desc", []string{"c.jpg", "b.jpg", "a.jpg"}},
		{"mtime", "asc", []string{"b.jpg", "c.jpg", "a.jpg"}},
		{"mtime", "desc", []string{"a.jpg", "c.jpg", "b.jpg"}},
	}
	for _, tc := range tests {
		idx := indices()
		sortIndices(idx, images, tc.by, tc.order)
		for i, want := range tc.want {
			if got := images[idx[i]].Path; got != want {
				t.Errorf("sortIndices(by=%q, order=%q)[%d] = %q, want %q", tc.by, tc.order, i, got, want)
			}
		}
	}
}

func TestSortIndicesTiebreaker(t *testing.T) {
	// When dates are equal, sort should fall back to path as a stable tiebreaker.
	same := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	images := []ImageInfo{
		{Path: "z.jpg", ModTime: same, FileMtime: same},
		{Path: "a.jpg", ModTime: same, FileMtime: same},
		{Path: "m.jpg", ModTime: same, FileMtime: same},
	}
	idx := []int{0, 1, 2}
	sortIndices(idx, images, "date", "desc")
	want := []string{"a.jpg", "m.jpg", "z.jpg"}
	for i, w := range want {
		if got := images[idx[i]].Path; got != w {
			t.Errorf("tiebreaker[%d] = %q, want %q", i, got, w)
		}
	}
}
