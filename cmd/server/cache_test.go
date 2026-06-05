package main

import (
	"testing"
	"time"
)

// insertTestFile is a helper that inserts a minimal file record into the test database.
func insertTestFile(t *testing.T, imgPath string, dateTaken, fileMtime time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO files (path, filename, folder, file_type, width, height, size, file_mtime, date_taken, indexed_at)
		 VALUES (?, ?, '', 'image', 0, 0, 0, ?, ?, ?)`,
		imgPath, imgPath, fileMtime.UnixNano(), dateTaken.UnixNano(), time.Now().UnixNano(),
	)
	if err != nil {
		t.Fatalf("insertTestFile %q: %v", imgPath, err)
	}
}

func TestQueryFilesSort(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		by    string
		order string
		want  []string
	}{
		{"name", "asc", []string{"a.jpg", "b.jpg", "c.jpg"}},
		{"name", "desc", []string{"c.jpg", "b.jpg", "a.jpg"}},
		{"taken", "asc", []string{"a.jpg", "b.jpg", "c.jpg"}},
		{"taken", "desc", []string{"c.jpg", "b.jpg", "a.jpg"}},
		{"mtime", "asc", []string{"b.jpg", "c.jpg", "a.jpg"}},
		{"mtime", "desc", []string{"a.jpg", "c.jpg", "b.jpg"}},
	}

	for _, tc := range tests {
		t.Run(tc.by+"_"+tc.order, func(t *testing.T) {
			setupTestEnv(t)
			// c.jpg: newest by taken, middle by mtime
			insertTestFile(t, "c.jpg", now.Add(-1*time.Hour), now.Add(-2*time.Hour))
			// a.jpg: oldest by taken, newest by mtime
			insertTestFile(t, "a.jpg", now.Add(-3*time.Hour), now.Add(-1*time.Hour))
			// b.jpg: middle by taken, oldest by mtime
			insertTestFile(t, "b.jpg", now.Add(-2*time.Hour), now.Add(-3*time.Hour))

			images, total, err := queryFiles(queryParams{sort: tc.by, order: tc.order})
			if err != nil {
				t.Fatalf("queryFiles: %v", err)
			}
			if total != 3 {
				t.Fatalf("total = %d, want 3", total)
			}
			for i, want := range tc.want {
				if got := images[i].Path; got != want {
					t.Errorf("[%d] = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestQueryFilesSortTiebreaker(t *testing.T) {
	setupTestEnv(t)
	// All three files have the same taken date — path should be the tiebreaker.
	same := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{"z.jpg", "a.jpg", "m.jpg"} {
		insertTestFile(t, name, same, same)
	}

	images, _, err := queryFiles(queryParams{sort: "taken", order: "desc"})
	if err != nil {
		t.Fatalf("queryFiles: %v", err)
	}
	want := []string{"a.jpg", "m.jpg", "z.jpg"}
	for i, w := range want {
		if got := images[i].Path; got != w {
			t.Errorf("tiebreaker[%d] = %q, want %q", i, got, w)
		}
	}
}
