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

// updateCachedFile evicts stale thumbnails and re-indexes a file that was
// created or modified externally. When addIfMissing is false and the file is
// not yet in the database it is still inserted.
func updateCachedFile(imgPath string, fi os.FileInfo, addIfMissing bool) {
	os.Remove(thumbSmallCachePath(imgPath))
	os.Remove(thumbMediumCachePath(imgPath))
	indexFileRecord(imgPath, 0, 0)
	if addIfMissing {
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
// and indexes all supported files inside it using a 4-worker pool.
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
		log.Printf("index-dir: completed %s", dirPath)
	})
}

// watchPhotosDir watches photosDir and all subdirectories with fsnotify,
// keeping the database in sync when files are added, modified, or removed.
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
						time.Sleep(200 * time.Millisecond)
						fi, err := os.Stat(event.Name)
						if err != nil {
							return
						}
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
						deleteFile(imgPath)
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

// reconcile cross-checks the database against the photos directory, evicts
// stale thumbnail files for photos that are gone, and re-syncs everything else.
func reconcile() {
	log.Println("reconcile: starting nightly consistency check")

	syncFilesToDB(func(imgPath string) {
		os.Remove(thumbSmallCachePath(imgPath))
		os.Remove(thumbMediumCachePath(imgPath))
		fileMu.Delete(imgPath)
		log.Printf("reconcile: evicted stale %s", imgPath)
	})

	log.Println("reconcile: complete")
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
