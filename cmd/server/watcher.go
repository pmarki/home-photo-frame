package main

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
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

// ── Event debouncer ───────────────────────────────────────────────────
//
// fsnotify emits many Write events while a single file is being copied in
// (cp/rsync/Samba). Without debouncing we evict thumbnails and re-index
// hundreds of times for one logical change. scheduleProcessing collapses
// repeated events on the same path into a single deferred call after
// debounceQuiet of silence, which also sidesteps the partial-Stat problem
// of reacting immediately to a Create that's still being written.

const debounceQuiet = 1 * time.Second

type pendingProcess struct {
	timer        *time.Timer
	addIfMissing bool
}

var pending = struct {
	sync.Mutex
	m map[string]*pendingProcess
}{m: make(map[string]*pendingProcess)}

func scheduleProcessing(imgPath, absPath string, addIfMissing bool) {
	pending.Lock()
	defer pending.Unlock()
	e, ok := pending.m[imgPath]
	if !ok {
		e = &pendingProcess{}
		pending.m[imgPath] = e
	}
	if addIfMissing {
		e.addIfMissing = true
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(debounceQuiet, func() {
		pending.Lock()
		// Stale-timer guard: a fresh scheduleProcessing for the same path
		// will have replaced e.timer; bail without touching the map so the
		// successor timer owns processing and cleanup.
		cur, ok := pending.m[imgPath]
		if !ok || cur != e || cur.timer != t {
			pending.Unlock()
			return
		}
		addIfMissing := cur.addIfMissing
		delete(pending.m, imgPath)
		pending.Unlock()

		fi, err := os.Stat(absPath)
		if err != nil {
			return
		}
		// Skip if our DB row already records this exact mtime — an upload
		// or crop handler self-indexed the file and re-running would just
		// evict the small thumb it generated inline.
		if dbMtimeMatches(imgPath, fi.ModTime()) {
			return
		}
		updateCachedFile(imgPath, fi, addIfMissing)
	})
	e.timer = t
}

func cancelProcessing(imgPath string) {
	pending.Lock()
	defer pending.Unlock()
	if e, ok := pending.m[imgPath]; ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(pending.m, imgPath)
	}
}

func dbMtimeMatches(imgPath string, mtime time.Time) bool {
	var ns int64
	if err := db.QueryRow(`SELECT file_mtime FROM files WHERE path = ?`, imgPath).Scan(&ns); err != nil {
		return false
	}
	return time.Unix(0, ns).Equal(mtime)
}

// watchedDirs tracks absolute paths of directories successfully added to the
// fsnotify watcher so we can explicitly Remove them on Rename/Remove events.
var watchedDirs sync.Map // string -> struct{}

// addWatchRecursive registers root and all its subdirectories with watcher.
// Returns counts so the caller can log a one-line summary.
func addWatchRecursive(watcher *fsnotify.Watcher, root string) (added, failed int) {
	var firstErr error
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil || !d.IsDir() {
			return nil
		}
		if werr := watcher.Add(p); werr != nil {
			if firstErr == nil {
				firstErr = werr
			}
			failed++
			log.Printf("watcher: failed to watch dir %s: %v", p, werr)
		} else {
			watchedDirs.Store(p, struct{}{})
			added++
		}
		return nil
	})
	if failed > 0 && firstErr != nil {
		if errors.Is(firstErr, syscall.ENOSPC) {
			log.Printf("watcher: hit inotify limit under %s — raise it on Linux with `sudo sysctl fs.inotify.max_user_watches=524288`", root)
		} else {
			log.Printf("watcher: first failure under %s: %v", root, firstErr)
		}
	}
	return added, failed
}

// removeWatchRecursive drops root and any tracked subdirectories from the
// fsnotify watcher. Safe to call when the directory has already been removed
// from disk — fsnotify.Remove just returns an error which we ignore.
func removeWatchRecursive(watcher *fsnotify.Watcher, root string) {
	prefix := root + string(filepath.Separator)
	watchedDirs.Delete(root)
	watcher.Remove(root) //nolint:errcheck
	watchedDirs.Range(func(k, _ any) bool {
		p := k.(string)
		if strings.HasPrefix(p, prefix) {
			watchedDirs.Delete(p)
			watcher.Remove(p) //nolint:errcheck
		}
		return true
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
	added, failed := addWatchRecursive(watcher, photosDir)
	log.Printf("watcher: watching %s — %d directories, %d failed", photosDir, added, failed)

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
							added, failed := addWatchRecursive(watcher, event.Name)
							if added > 1 || failed > 0 {
								log.Printf("watcher: added subtree %s — %d directories, %d failed", event.Name, added, failed)
							}
							indexNewlyArrivedDir(event.Name)
							return
						}
					}

					// Directory removal/rename: clean up our tracked watches
					// before the supportedExts filter drops the event. File
					// children inside the dir arrive as their own events.
					if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
						if _, isDir := watchedDirs.Load(event.Name); isDir {
							removeWatchRecursive(watcher, event.Name)
							return
						}
					}

					ext := strings.ToLower(filepath.Ext(event.Name))
					if !supportedExts[ext] {
						return
					}

					switch {
					case event.Has(fsnotify.Create):
						scheduleProcessing(imgPath, event.Name, true)

					case event.Has(fsnotify.Write):
						scheduleProcessing(imgPath, event.Name, false)

					case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
						cancelProcessing(imgPath)
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
