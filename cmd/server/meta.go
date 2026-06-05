package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// ── Image metadata index ──────────────────────────────────────────────────
// Persisted to {cacheDir}/meta.json. Maps filename → best available date,
// derived in priority order: EXIF → filename pattern → file mtime.

type ImageMeta struct {
	Date      time.Time `json:"date"`
	FileMtime time.Time `json:"fileMtime,omitempty"` // OS mtime, used for thumb cache-busting
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
}

var (
	metaIndex  map[string]ImageMeta
	metaMu     sync.RWMutex
	metaPath   string
	metaSaveCh = make(chan struct{}, 1)
)

// filenameDate matches patterns like 20190318_132033 at the start of the base name.
var filenameDate = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})`)

func loadMetaIndex() {
	metaIndex = map[string]ImageMeta{}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &metaIndex); err != nil {
		log.Printf("meta: parse error: %v", err)
		metaIndex = map[string]ImageMeta{}
	}
}

// saveMetaIndex signals the meta saver goroutine to write meta.json.
// Non-blocking: if a save is already pending the signal is dropped (the
// pending write will use the latest state of metaIndex when it runs).
func saveMetaIndex() {
	select {
	case metaSaveCh <- struct{}{}:
	default:
	}
}

// runMetaSaver processes save signals one at a time, preventing concurrent
// writes to meta.json. Must be started after metaPath is set.
func runMetaSaver() {
	for range metaSaveCh {
		metaMu.RLock()
		data, err := json.Marshal(metaIndex)
		metaMu.RUnlock()
		if err != nil {
			log.Printf("meta: marshal error: %v", err)
			continue
		}
		tmp := metaPath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			log.Printf("meta: write error: %v", err)
			continue
		}
		if err := os.Rename(tmp, metaPath); err != nil {
			log.Printf("meta: rename error: %v", err)
		}
	}
}

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

// indexImage extracts and stores the best date, OS mtime, and (optionally) dimensions
// for imgPath (a "/" -separated path relative to photosDir). Pass w=0,h=0 when
// dimensions are not yet known.
func indexImage(imgPath string, w, h int) {
	srcPath := filepath.Join(photosDir, filepath.FromSlash(imgPath))

	var fileMtime time.Time
	if fi, err := os.Stat(srcPath); err == nil {
		fileMtime = fi.ModTime()
	}

	metaMu.RLock()
	existing, exists := metaIndex[imgPath]
	metaMu.RUnlock()

	// Skip if fully indexed, no new dimensions, and file hasn't changed.
	if exists && existing.Width > 0 && w == 0 &&
		!fileMtime.IsZero() && existing.FileMtime.Equal(fileMtime) {
		return
	}

	// Re-extract date if file is new or its mtime changed.
	date := existing.Date
	if !exists || date.IsZero() || (!fileMtime.IsZero() && !existing.FileMtime.Equal(fileMtime)) {
		date = extractBestDate(imgPath, srcPath)
	}

	width, height := existing.Width, existing.Height
	if w > 0 {
		width, height = w, h
	}

	metaMu.Lock()
	metaIndex[imgPath] = ImageMeta{Date: date, FileMtime: fileMtime, Width: width, Height: height}
	metaMu.Unlock()

	if w > 0 {
		cacheUpdateDimensions(imgPath, width, height)
	}
}

// bestDate returns the indexed date for imgPath, or fallback if not in index.
func bestDate(imgPath string, fallback time.Time) time.Time {
	metaMu.RLock()
	meta, ok := metaIndex[imgPath]
	metaMu.RUnlock()
	if ok {
		return meta.Date
	}
	return fallback
}
