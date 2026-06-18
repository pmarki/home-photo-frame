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

// filenameDatePrefixed matches the common camera convention with a letter prefix,
// e.g. IMG_20151008_115901371.jpg, VID_20190318_132033.mp4, PXL_20231115_180004.jpg.
// The optional trailing digits (milliseconds on Android/Pixel) are tolerated by
// the rest of the regex being unanchored at the end.
var filenameDatePrefixed = regexp.MustCompile(`^[A-Za-z]+_(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})`)

// filenameDateWhatsApp matches WhatsApp's media naming, e.g. IMG-20240101-WA0001.jpg
// or VID-20240101-WA0001.mp4. WhatsApp filenames carry only the date — no time —
// so this pattern produces 3 capture groups instead of 6; the time defaults to
// midnight when this pattern is the source of the date.
var filenameDateWhatsApp = regexp.MustCompile(`^[A-Za-z]+-(\d{4})(\d{2})(\d{2})-WA\d+`)

// exifDate opens srcPath and returns DateTimeOriginal / DateTime from EXIF.
// The file handle is closed via defer so an EXIF library panic does not leak it.
func exifDate(srcPath string) (time.Time, bool) {
	f, err := os.Open(srcPath)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return time.Time{}, false
	}
	t, err := x.DateTime()
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func extractBestDate(filename, srcPath string) time.Time {
	// 1. EXIF DateTimeOriginal / DateTime (images only — videos have no EXIF)
	if !isVideo(filename) {
		if t, ok := exifDate(srcPath); ok {
			return t
		}
	}
	// 2. Filename patterns: YYYYMMDD_HHMMSS at start of base name (with or
	// without a letter prefix like IMG_, VID_, PXL_), plus WhatsApp's
	// IMG-YYYYMMDD-WAxxxx convention which has no time component.
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	for _, re := range []*regexp.Regexp{filenameDate, filenameDatePrefixed, filenameDateWhatsApp} {
		if m := re.FindStringSubmatch(base); m != nil {
			var s string
			if len(m) >= 7 {
				s = m[1] + m[2] + m[3] + m[4] + m[5] + m[6]
			} else {
				s = m[1] + m[2] + m[3] + "000000"
			}
			if t, err := time.ParseInLocation("20060102150405", s, time.Local); err == nil {
				return t
			}
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
