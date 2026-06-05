package main

import (
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Image list ────────────────────────────────────────────────────────────

type ImageInfo struct {
	Filename    string    `json:"filename"`        // basename only, e.g. "photo.jpg"
	Path        string    `json:"path"`            // relative path, e.g. "vacation/photo.jpg"
	ModTime     time.Time `json:"modTime"`         // best date: EXIF → filename pattern → mtime
	FileMtime   time.Time `json:"-"`               // OS mtime, used for "date modified" sort
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

// ── In-memory images cache ─────────────────────────────────────────────────
// Populated once from disk and updated incrementally on mutations.
// Avoids re-reading the photos directory on every /api/images request.

var (
	imagesCache   []ImageInfo
	imageCacheSet map[string]struct{} // path → present; kept in sync with imagesCache
	imagesCacheMu sync.RWMutex
	sortedCache   map[string][]int // keyed "by:order"; nil means invalid, cleared on any mutation
)

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

// buildImagesCache reads the photos directory recursively and rebuilds imagesCache
// from scratch. Called at startup (after loadMetaIndex) so the gallery is
// immediately available, and again at the end of warmup to pick up any newly
// discovered metadata.
func buildImagesCache() {
	log.Println("scan: building image cache")
	list := make([]ImageInfo, 0, 256)
	lastDir := ""
	walkPhotosDir(func(imgPath string, fi os.FileInfo) {
		if dir := path.Dir(imgPath); dir != lastDir {
			lastDir = dir
			log.Printf("scan: indexing %s", dir)
		}
		metaMu.RLock()
		meta := metaIndex[imgPath]
		metaMu.RUnlock()
		fileMtime := meta.FileMtime
		if fileMtime.IsZero() {
			fileMtime = fi.ModTime()
		}
		small, medium, original := thumbURLs(imgPath)
		list = append(list, ImageInfo{
			Filename:    filepath.Base(imgPath),
			Path:        imgPath,
			ModTime:     bestDate(imgPath, fi.ModTime()),
			FileMtime:   fileMtime,
			Size:        fi.Size(),
			Width:       meta.Width,
			Height:      meta.Height,
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
		})
	})
	set := make(map[string]struct{}, len(list))
	for _, img := range list {
		set[img.Path] = struct{}{}
	}
	imagesCacheMu.Lock()
	imagesCache = list
	imageCacheSet = set
	sortedCache = nil
	imagesCacheMu.Unlock()
	log.Printf("scan: image cache built (%d files)", len(list))
}

func cacheAdd(info ImageInfo) {
	imagesCacheMu.Lock()
	if _, exists := imageCacheSet[info.Path]; exists {
		imagesCacheMu.Unlock()
		return
	}
	imagesCache = append(imagesCache, info)
	if imageCacheSet == nil {
		imageCacheSet = make(map[string]struct{})
	}
	imageCacheSet[info.Path] = struct{}{}
	sortedCache = nil
	imagesCacheMu.Unlock()
}

func cacheRemove(imgPath string) {
	imagesCacheMu.Lock()
	for i, img := range imagesCache {
		if img.Path == imgPath {
			imagesCache = append(imagesCache[:i], imagesCache[i+1:]...)
			delete(imageCacheSet, imgPath)
			break
		}
	}
	sortedCache = nil
	imagesCacheMu.Unlock()
}

func cacheUpdateDimensions(imgPath string, w, h int) {
	small, medium, original := thumbURLs(imgPath)
	imagesCacheMu.Lock()
	for i, img := range imagesCache {
		if img.Path == imgPath {
			imagesCache[i].Width = w
			imagesCache[i].Height = h
			imagesCache[i].ThumbSmall = small
			imagesCache[i].ThumbMedium = medium
			imagesCache[i].Original = original
			break
		}
	}
	sortedCache = nil
	imagesCacheMu.Unlock()
}

// sortIndices sorts images in place. Uses SliceStable with a path tiebreaker
// so pagination is deterministic even when multiple images share the same date.
func sortIndices(indices []int, images []ImageInfo, by, order string) {
	switch by {
	case "name":
		if order == "desc" {
			sort.SliceStable(indices, func(i, j int) bool { return images[indices[i]].Path > images[indices[j]].Path })
		} else {
			sort.SliceStable(indices, func(i, j int) bool { return images[indices[i]].Path < images[indices[j]].Path })
		}
	case "mtime":
		if order == "asc" {
			sort.SliceStable(indices, func(i, j int) bool {
				a, b := images[indices[i]], images[indices[j]]
				if a.FileMtime.Equal(b.FileMtime) {
					return a.Path < b.Path
				}
				return a.FileMtime.Before(b.FileMtime)
			})
		} else {
			sort.SliceStable(indices, func(i, j int) bool {
				a, b := images[indices[i]], images[indices[j]]
				if a.FileMtime.Equal(b.FileMtime) {
					return a.Path < b.Path
				}
				return a.FileMtime.After(b.FileMtime)
			})
		}
	default: // "taken", "date", or unspecified → sort by EXIF/best date
		if order == "asc" {
			sort.SliceStable(indices, func(i, j int) bool {
				a, b := images[indices[i]], images[indices[j]]
				if a.ModTime.Equal(b.ModTime) {
					return a.Path < b.Path
				}
				return a.ModTime.Before(b.ModTime)
			})
		} else {
			sort.SliceStable(indices, func(i, j int) bool {
				a, b := images[indices[i]], images[indices[j]]
				if a.ModTime.Equal(b.ModTime) {
					return a.Path < b.Path
				}
				return a.ModTime.After(b.ModTime)
			})
		}
	}
}
