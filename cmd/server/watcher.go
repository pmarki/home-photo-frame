package main

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// updateCachedFile refreshes in-memory state for a file that was created or
// modified externally: evicts stale thumbnails, re-indexes, and updates
// imagesCache. When addIfMissing is true the file is also added to the cache
// if it was not already present (used for Create events).
func updateCachedFile(imgPath string, fi os.FileInfo, addIfMissing bool) {
	os.Remove(thumbSmallCachePath(imgPath))
	os.Remove(thumbMediumCachePath(imgPath))
	indexImage(imgPath, 0, 0)
	saveMetaIndex()
	small, medium, original := thumbURLs(imgPath)
	metaMu.RLock()
	meta := metaIndex[imgPath]
	metaMu.RUnlock()

	imagesCacheMu.Lock()
	found := false
	for i, img := range imagesCache {
		if img.Path == imgPath {
			imagesCache[i].ModTime = bestDate(imgPath, fi.ModTime())
			imagesCache[i].FileMtime = fi.ModTime()
			imagesCache[i].Size = fi.Size()
			imagesCache[i].Width = meta.Width
			imagesCache[i].Height = meta.Height
			imagesCache[i].ThumbSmall = small
			imagesCache[i].ThumbMedium = medium
			imagesCache[i].Original = original
			sortedCache = nil
			found = true
			break
		}
	}
	imagesCacheMu.Unlock()

	if addIfMissing && !found {
		cacheAdd(ImageInfo{
			Filename:    filepath.Base(imgPath),
			Path:        imgPath,
			ModTime:     bestDate(imgPath, fi.ModTime()),
			FileMtime:   fi.ModTime(),
			Size:        fi.Size(),
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
		})
		log.Printf("watcher: added %s", imgPath)
	} else {
		log.Printf("watcher: updated %s", imgPath)
	}
}

// addWatchRecursive registers root and all its subdirectories with watcher.
func addWatchRecursive(watcher *fsnotify.Watcher, root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err == nil && d.IsDir() {
			if werr := watcher.Add(p); werr != nil {
				log.Printf("watcher: failed to watch dir %s: %v", p, werr)
			}
		}
		return nil
	})
}

// indexNewlyArrivedDir walks a directory that just appeared (e.g. via rename/mv)
// and indexes all supported files inside it. It runs in a background goroutine
// with a 4-worker pool so the watcher event loop is not blocked and disk I/O
// stays throttled when large trees arrive at once.
func indexNewlyArrivedDir(dirPath string) {
	safeGo("index-dir", func() {
		log.Printf("index-dir: scanning %s", dirPath)
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		filepath.WalkDir(dirPath, func(absPath string, d fs.DirEntry, err error) error { //nolint:errcheck
			if err != nil || d.IsDir() {
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
			imgPath := filepath.ToSlash(rel)
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				updateCachedFile(imgPath, fi, true)
			}()
			return nil
		})
		wg.Wait()
		saveMetaIndex()
		log.Printf("index-dir: completed %s", dirPath)
	})
}

// watchPhotosDir watches photosDir and all subdirectories with fsnotify,
// keeping imagesCache and metaIndex in sync when files are added, modified,
// or removed externally.
func watchPhotosDir() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watcher: failed to create: %v", err)
		return
	}
	addWatchRecursive(watcher, photosDir)
	log.Printf("watcher: watching %s (recursive)", photosDir)

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							log.Printf("panic in watcher event handler: %v\n%s", rec, debug.Stack())
						}
					}()

					rel, err := filepath.Rel(photosDir, event.Name)
					if err != nil {
						return
					}
					imgPath := filepath.ToSlash(rel)

					// Handle directory creation: start watching the new subtree.
					if event.Has(fsnotify.Create) {
						fi, statErr := os.Stat(event.Name)
						if statErr == nil && fi.IsDir() {
							addWatchRecursive(watcher, event.Name)
							indexNewlyArrivedDir(event.Name)
							return
						}
					}

					ext := strings.ToLower(filepath.Ext(event.Name))
					if !supportedExts[ext] {
						return
					}

					switch {
					case event.Has(fsnotify.Create):
						// Wait briefly for the file to be fully written.
						time.Sleep(200 * time.Millisecond)
						fi, err := os.Stat(event.Name)
						if err != nil {
							return
						}
						// Do NOT delete metaIndex entry: indexImage preserves existing
						// Width/Height when called with w=0, and detects mtime changes.
						updateCachedFile(imgPath, fi, true)

					case event.Has(fsnotify.Write):
						fi, err := os.Stat(event.Name)
						if err != nil {
							return
						}
						updateCachedFile(imgPath, fi, false)

					case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
						os.Remove(thumbSmallCachePath(imgPath))
						os.Remove(thumbMediumCachePath(imgPath))
						fileMu.Delete(imgPath)
						metaMu.Lock()
						delete(metaIndex, imgPath)
						metaMu.Unlock()
						cacheRemove(imgPath)
						saveMetaIndex()
						log.Printf("watcher: removed %s", imgPath)
					}
				}()

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("watcher: error: %v", err)
			}
		}
	}()
}

// reconcile cross-checks the photos directory, imagesCache, and metaIndex and
// brings them into agreement. It removes stale in-memory entries and thumbnail
// files for photos deleted from disk, and adds cache entries for any photos
// that are on disk but missing from the in-memory index.
func reconcile() {
	log.Println("reconcile: starting nightly consistency check")

	onDisk := make(map[string]os.FileInfo, 256)
	walkPhotosDir(func(imgPath string, fi os.FileInfo) {
		onDisk[imgPath] = fi
	})

	changed := false

	// Collect cache entries whose files are gone; also build the inCache set.
	imagesCacheMu.RLock()
	var stale []string
	inCache := make(map[string]bool, len(imagesCache))
	for _, img := range imagesCache {
		if _, ok := onDisk[img.Path]; ok {
			inCache[img.Path] = true
		} else {
			stale = append(stale, img.Path)
		}
	}
	imagesCacheMu.RUnlock()

	// Evict stale entries without holding imagesCacheMu across metaMu.
	for _, imgPath := range stale {
		os.Remove(thumbSmallCachePath(imgPath))
		os.Remove(thumbMediumCachePath(imgPath))
		fileMu.Delete(imgPath)
		cacheRemove(imgPath)
		metaMu.Lock()
		delete(metaIndex, imgPath)
		metaMu.Unlock()
		log.Printf("reconcile: evicted stale entry %s", imgPath)
		changed = true
	}

	// Add files on disk that are missing from the cache.
	for imgPath, fi := range onDisk {
		if inCache[imgPath] {
			continue
		}
		indexImage(imgPath, 0, 0)
		small, medium, original := thumbURLs(imgPath)
		cacheAdd(ImageInfo{
			Filename:    filepath.Base(imgPath),
			Path:        imgPath,
			ModTime:     bestDate(imgPath, fi.ModTime()),
			FileMtime:   fi.ModTime(),
			Size:        fi.Size(),
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
		})
		log.Printf("reconcile: added missing file %s", imgPath)
		changed = true
	}

	// Prune orphaned metaIndex entries (no corresponding file on disk).
	// Collect under a read lock, then delete one at a time so readers are
	// not blocked for the full scan.
	metaMu.RLock()
	var orphaned []string
	for imgPath := range metaIndex {
		if _, ok := onDisk[imgPath]; !ok {
			orphaned = append(orphaned, imgPath)
		}
	}
	metaMu.RUnlock()
	for _, imgPath := range orphaned {
		metaMu.Lock()
		delete(metaIndex, imgPath)
		metaMu.Unlock()
		changed = true
	}

	if changed {
		saveMetaIndex()
		log.Println("reconcile: completed with changes")
	} else {
		log.Println("reconcile: completed, everything aligned")
	}
}

// scheduleNightlyReconcile runs reconcile every day at 03:00 local time.
func scheduleNightlyReconcile() {
	go func() {
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
			if !next.After(now) {
				next = next.Add(24 * time.Hour)
			}
			time.Sleep(time.Until(next))
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("panic in reconcile: %v\n%s", rec, debug.Stack())
					}
				}()
				reconcile()
			}()
		}
	}()
}
