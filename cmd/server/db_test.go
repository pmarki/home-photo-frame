package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// seedIndexed writes a tiny JPEG at each relative path and indexes it.
func seedIndexed(t *testing.T, paths ...string) {
	t.Helper()
	img := makeTestJPEG(4, 4)
	for _, p := range paths {
		abs := filepath.Join(photosDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, img, 0o644); err != nil {
			t.Fatal(err)
		}
		indexFileRecord(p, 4, 4)
	}
}

func queryPaths(t *testing.T, p queryParams) []string {
	t.Helper()
	images, _, err := queryFiles(p)
	if err != nil {
		t.Fatalf("queryFiles: %v", err)
	}
	var out []string
	for _, im := range images {
		out = append(out, im.Path)
	}
	sort.Strings(out)
	return out
}

func equalPaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestQueryFilesFolderFiltersAreLiteral(t *testing.T) {
	setupTestEnv(t)
	seedIndexed(t, "a_b/1.jpg", "a_b/sub/2.jpg", "axb/3.jpg", "a_b0/4.jpg", "100%/5.jpg", "1000/6.jpg")

	if got, want := queryPaths(t, queryParams{folder: "a_b"}), []string{"a_b/1.jpg", "a_b/sub/2.jpg"}; !equalPaths(got, want) {
		t.Errorf("folder a_b: got %v, want %v", got, want)
	}
	if got, want := queryPaths(t, queryParams{folder: "100%"}), []string{"100%/5.jpg"}; !equalPaths(got, want) {
		t.Errorf("folder 100%%: got %v, want %v", got, want)
	}
	if got, want := queryPaths(t, queryParams{ownerFolders: []string{"a_b"}}), []string{"a_b/1.jpg", "a_b/sub/2.jpg"}; !equalPaths(got, want) {
		t.Errorf("owner a_b: got %v, want %v", got, want)
	}
	got := queryPaths(t, queryParams{deniedTopFolders: []string{"a_b"}})
	if want := []string{"100%/5.jpg", "1000/6.jpg", "a_b0/4.jpg", "axb/3.jpg"}; !equalPaths(got, want) {
		t.Errorf("denied a_b: got %v, want %v", got, want)
	}
}

func TestQueryFilesSearchEscapesWildcards(t *testing.T) {
	setupTestEnv(t)
	seedIndexed(t, "100%_sun.jpg", "1000_sun.jpg", "10Xsun.jpg", "back\\slash.jpg", "backXslash.jpg")

	if got, want := queryPaths(t, queryParams{search: "100%"}), []string{"100%_sun.jpg"}; !equalPaths(got, want) {
		t.Errorf("search 100%%: got %v, want %v", got, want)
	}
	if got, want := queryPaths(t, queryParams{search: "0_s"}), []string{"1000_sun.jpg"}; !equalPaths(got, want) {
		t.Errorf("search 0_s: got %v, want %v", got, want)
	}
	if got, want := queryPaths(t, queryParams{search: `k\s`}), []string{`back\slash.jpg`}; !equalPaths(got, want) {
		t.Errorf(`search k\s: got %v, want %v`, got, want)
	}
}

func TestDeleteFileRemovesAllRows(t *testing.T) {
	setupTestEnv(t)
	seedIndexed(t, "p.jpg")
	setFavorite("p.jpg", "u1", true) //nolint:errcheck
	trackView("p.jpg", nil)

	deleteFile("p.jpg")

	var n int
	db.QueryRow(`SELECT (SELECT COUNT(*) FROM files) + (SELECT COUNT(*) FROM file_views) + (SELECT COUNT(*) FROM file_favorites)`).Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Errorf("%d rows left after deleteFile", n)
	}
}

func TestReadPool(t *testing.T) {
	dir := t.TempDir()
	w, err := openDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r, err := openReadDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if _, err := w.Exec(`INSERT INTO file_favorites (file_path, user_id, favorited_at) VALUES ('a', '', 1)`); err != nil {
		t.Fatal(err)
	}

	// Hold the single writer connection in an open write transaction; reads
	// on the pool must still complete (they would block forever on the writer).
	tx, err := w.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`INSERT INTO file_favorites (file_path, user_id, favorited_at) VALUES ('b', '', 1)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		var n int
		r.QueryRow(`SELECT COUNT(*) FROM file_favorites`).Scan(&n) //nolint:errcheck
		done <- n
	}()
	select {
	case n := <-done:
		if n != 1 {
			t.Errorf("read pool saw %d rows, want 1 (committed only)", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read blocked behind open write transaction")
	}

	if _, err := r.Exec(`DELETE FROM file_favorites`); err == nil {
		t.Error("read pool accepted a write; query_only not applied")
	}
}
