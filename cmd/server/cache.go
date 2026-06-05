package main

import (
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ── Image list ────────────────────────────────────────────────────────────

type ImageInfo struct {
	Filename    string    `json:"filename"`
	Path        string    `json:"path"`
	ModTime     time.Time `json:"modTime"`   // best date: EXIF → filename pattern → mtime
	FileMtime   time.Time `json:"-"`         // OS mtime, used for "date modified" sort
	Size        int64     `json:"size"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
	ThumbSmall  string    `json:"thumbSmall"`
	ThumbMedium string    `json:"thumbMedium"`
	Original    string    `json:"original"`
}

type ListResponse struct {
	Images []ImageInfo `json:"images"`
	Total  int         `json:"total"`
	Page   int         `json:"page"`
	Limit  int         `json:"limit"`
}

// walkPhotosDir calls fn for every supported media file found recursively under
// photosDir. imgPath is the "/" -separated path relative to photosDir.
func walkPhotosDir(fn func(imgPath string, fi os.FileInfo)) {
	filepath.WalkDir(photosDir, func(absPath string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil {
			log.Printf("walk: %s: %v", absPath, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !supportedExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(photosDir, absPath)
		if err != nil {
			return nil
		}
		fn(filepath.ToSlash(rel), fi)
		return nil
	})
}

// syncFilesToDB walks photosDir and ensures the database is in sync with the
// filesystem. Files whose path+mtime already match a database record are
// skipped. Records for files that no longer exist on disk are deleted; if
// onEvict is non-nil it is called before each deletion (use it to remove
// thumbnail cache files).
func syncFilesToDB(onEvict func(imgPath string)) {
	log.Println("scan: syncing files to database")

	// Pre-load existing records: path → stored mtime, for fast comparison.
	existing := make(map[string]time.Time)
	if rows, err := db.Query(`SELECT path, file_mtime FROM files`); err == nil {
		for rows.Next() {
			var p string
			var ns int64
			if rows.Scan(&p, &ns) == nil {
				existing[p] = time.Unix(0, ns)
			}
		}
		rows.Close()
	}

	seen := make(map[string]struct{}, len(existing))
	count, updated := 0, 0
	lastDir := ""

	walkPhotosDir(func(imgPath string, fi os.FileInfo) {
		if dir := path.Dir(imgPath); dir != lastDir {
			lastDir = dir
			log.Printf("scan: indexing %s", dir)
		}
		seen[imgPath] = struct{}{}
		if storedMtime, ok := existing[imgPath]; ok && storedMtime.Equal(fi.ModTime()) {
			count++
			return
		}
		indexFileRecord(imgPath, 0, 0)
		count++
		updated++
	})

	// Remove database entries for files no longer on disk.
	for p := range existing {
		if _, ok := seen[p]; !ok {
			if onEvict != nil {
				onEvict(p)
			}
			deleteFile(p)
		}
	}

	var total int
	db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&total) //nolint:errcheck
	log.Printf("scan: complete — %d files scanned, %d updated, %d in database", count, updated, total)
}
