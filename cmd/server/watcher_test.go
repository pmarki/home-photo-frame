package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvictFolder(t *testing.T) {
	setupTestEnv(t)
	for _, d := range []string{"trip/day1", "trip2", "tripx"} {
		if err := os.MkdirAll(filepath.Join(photosDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	img := makeTestJPEG(4, 4)
	for _, p := range []string{"trip/a.jpg", "trip/day1/b.jpg", "trip2/c.jpg", "tripx/d.jpg"} {
		if err := os.WriteFile(filepath.Join(photosDir, p), img, 0o644); err != nil {
			t.Fatal(err)
		}
		indexFileRecord(p, 4, 4)
		setFavorite(p, "", true) //nolint:errcheck
	}
	cached := thumbSmallCachePath("trip/day1/b.jpg")
	os.MkdirAll(filepath.Dir(cached), 0o755) //nolint:errcheck
	os.WriteFile(cached, img, 0o644)         //nolint:errcheck

	evictFolder("trip")

	var files, favs int
	db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&files)         //nolint:errcheck
	db.QueryRow(`SELECT COUNT(*) FROM file_favorites`).Scan(&favs) //nolint:errcheck
	if files != 2 || favs != 2 {
		t.Errorf("files=%d favorites=%d, want 2 and 2 (trip2 and tripx untouched)", files, favs)
	}
	if _, err := os.Stat(cached); err == nil {
		t.Error("cached thumbnail under evicted folder still exists")
	}
}

func TestEvictFolderRejectsInvalidPath(t *testing.T) {
	setupTestEnv(t)
	keep := filepath.Join(cacheDir, "s", "keep.jpg")
	os.WriteFile(keep, []byte("x"), 0o644) //nolint:errcheck
	for _, p := range []string{"", ".", ".."} {
		evictFolder(p)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("cache root was removed")
	}
}

func TestIndexArrivedFileSkipsAlreadyIndexed(t *testing.T) {
	setupTestEnv(t)
	seedIndexed(t, "album/1.jpg")
	thumb := thumbSmallCachePath("album/1.jpg")
	os.MkdirAll(filepath.Dir(thumb), 0o755) //nolint:errcheck
	os.WriteFile(thumb, []byte("t"), 0o644) //nolint:errcheck

	fi, _ := os.Stat(filepath.Join(photosDir, "album", "1.jpg"))
	if indexArrivedFile("album/1.jpg", fi) {
		t.Error("file with matching DB mtime was re-indexed")
	}
	if _, err := os.Stat(thumb); err != nil {
		t.Error("cached thumbnail was evicted")
	}

	// A file the DB doesn't know yet is indexed.
	os.WriteFile(filepath.Join(photosDir, "album", "2.jpg"), makeTestJPEG(4, 4), 0o644) //nolint:errcheck
	fi2, _ := os.Stat(filepath.Join(photosDir, "album", "2.jpg"))
	if !indexArrivedFile("album/2.jpg", fi2) {
		t.Error("new file was not indexed")
	}
}
