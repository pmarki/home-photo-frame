package main

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// filenameDate matches patterns like 20190318_132033 at the start of the base name.
var filenameDate = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})`)

func extractBestDate(filename, srcPath string) time.Time {
	// 1. EXIF DateTimeOriginal / DateTime (images only — videos have no EXIF)
	if !isVideo(filename) {
		if f, err := os.Open(srcPath); err == nil {
			if x, err := exif.Decode(f); err == nil {
				if t, err := x.DateTime(); err == nil {
					f.Close()
					return t
				}
			}
			f.Close()
		}
	}
	// 2. Filename pattern: YYYYMMDD_HHMMSS at start of base name
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if m := filenameDate.FindStringSubmatch(base); m != nil {
		s := m[1] + m[2] + m[3] + m[4] + m[5] + m[6]
		if t, err := time.ParseInLocation("20060102150405", s, time.Local); err == nil {
			return t
		}
	}
	// 3. File mtime
	if fi, err := os.Stat(srcPath); err == nil {
		return fi.ModTime()
	}
	return time.Now()
}

// indexFileRecord re-extracts metadata for imgPath and upserts it into the
// database. Pass w>0, h>0 when dimensions are already known; passing 0 preserves
// whatever dimensions are already stored. Skips date re-extraction when the
// file's mtime is unchanged from the stored record.
func indexFileRecord(imgPath string, w, h int) {
	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))
	fi, err := os.Stat(srcPath)
	if err != nil {
		return
	}

	var existingMtimeNs, existingDateNs int64
	dbErr := db.QueryRow(
		`SELECT file_mtime, date_taken FROM files WHERE path = ?`, imgPath,
	).Scan(&existingMtimeNs, &existingDateNs)

	var dateTaken time.Time
	if dbErr == nil && time.Unix(0, existingMtimeNs).Equal(fi.ModTime()) {
		dateTaken = time.Unix(0, existingDateNs)
	} else {
		dateTaken = extractBestDate(imgPath, srcPath)
	}

	if err := upsertFile(imgPath, fi, dateTaken, w, h); err != nil {
		log.Printf("index: upsert %s: %v", imgPath, err)
	}
}
