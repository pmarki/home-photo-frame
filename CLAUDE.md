# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Development (two terminals)
make backend-dev        # Go server on :8080, reads frontend/dist from disk (-tags dev)
make frontend-dev       # Vite dev server on :5173, proxies /api → :8080

# Tests
make test               # go test -race ./cmd/server/

# Production
make build              # builds frontend, copies to cmd/server/frontend/dist, compiles binary
./photo-frame -photos ./photos -cache ./cache

# Single test
/home/piotrek/sdk/go1.22.5/bin/go test -race -run TestFunctionName ./cmd/server/
```

`make backend-dev` uses `-tags dev` which bypasses `go:embed` — the binary reads `./frontend/dist` from disk instead of embedding it, so the frontend does not need to be built first.

## Architecture

### Go backend (`cmd/server/`)

All server code is in a single `package main`. Source files by responsibility:

| File | Responsibility |
|---|---|
| `main.go` | Flag parsing, directory setup, HTTP route registration, graceful shutdown |
| `db.go` | SQLite schema, `openDB`, `upsertFile`, `deleteFile`, `queryFiles`/`queryParams` |
| `cache.go` | `ImageInfo`/`ListResponse` types, `walkPhotosDir`, `syncFilesToDB` |
| `handlers.go` | All HTTP handlers (`handleImages`, `handleUpload`, `handleCrop`, `handleDelete`, etc.) |
| `thumbnails.go` | `thumbHash`, `thumbURLs`, `serveCachedThumb`, `warmupThumbnails`, striped file locks (`lockFile`/`lockFiles`) |
| `watcher.go` | `watchPhotosDir` (fsnotify), `reconcile`, `scheduleNightlyReconcile` (03:00 daily) |
| `meta.go` | `extractBestDate` (EXIF → filename pattern → mtime), `indexFileRecord` |
| `jpeg.go` | Raw JPEG EXIF manipulation: extract/inject APP1, reset IFD0 orientation + zero IFD1 |
| `validation.go` | `isValidPath`, `isValidFilename`, `encodePathSegments`, `extractVideoFrame` (ffmpeg) |
| `middleware.go` | `recoveryMiddleware`, `corsMiddleware`, `safeGo`, `safeLoop` |
| `embed.go` | `//go:embed all:frontend/dist` (production, build tag `!dev`) |
| `embed_dev.go` | `os.DirFS("./frontend/dist")` fallback (build tag `dev`) |

### Key design decisions

**SQLite concurrency**: `db` is the writer — `SetMaxOpenConns(1)` serializes all writes through Go's pool mutex, preventing `SQLITE_BUSY`. Read-only queries (image list, folders, stats, bulk preloads) go through `readDB()`, a 4-connection pool opened with `query_only` so they run concurrently with writes under WAL. In tests `dbRead` is nil (an in-memory DB can't be shared across connections) and `readDB()` falls back to `db`. Never hold a writer `rows` open while issuing another writer query — the single connection deadlocks.

**Folder filters**: subtree matches use `subtreeCond` (`folder = ? OR folder >= f/ AND folder < f0`) instead of `LIKE`, so they are index-backed and literal; filename search escapes `%`/`_`/`\` with `likeEscaper` + `ESCAPE '\'`.

**Content-addressed URLs**: `thumbHash(path, mtime)` = first 8 bytes of SHA256(path + "\x00" + mtime_ns). URLs are `immutable`-cached for 1 year; the hash changes only when the source file's mtime changes, so browsers auto-fetch fresh content.

**Thumbnail cache layout**: `cache/s/{relative_path}` (400 px small) and `cache/m/{relative_path}` (up to `mediumWidth` px). Cache paths mirror the source directory structure using the original filename.

**File locks** (`fileLocks [256]sync.Mutex`): paths hash (FNV-1a) onto fixed stripes. Crop, upload, thumbnail generation, warmup, delete and move/copy take the lock for each file they touch, so work on one file is serialized and the destination-exists check in move/copy is atomic with uploads. Entries are never deleted, so a held lock can't be split. Use `lockFiles(a, b)` — never two nested `lockFile` calls — when holding several paths: it locks stripes in ascending order and dedupes paths that share a stripe.

**Temp files**: `writeFileAtomic` creates `.pf-*.tmp` beside its target; `sweepTempFiles` removes ones older than an hour (plus legacy `<photo>.<ext>.tmp`) from the photos and cache dirs at startup and in the nightly reconcile.

**Crop flow**: reads original → `imaging.AutoOrientation` decodes → crop → re-encode → strip IFD1 thumbnail from EXIF APP1 and reset IFD0 orientation to 1 (since pixels already have rotation baked in) → fsynced atomic write (`writeFileAtomic`: temp file + rename) → delete stale cache files → re-index.

**Decode limits**: every full image decode (warmup, on-demand thumbnails, upload, crop) goes through `openSourceImage`/`acquireDecode`, which share the process-wide `decodeSem` (`maxConcurrentDecodes` = 3) and reject images over `-max-megapixels` (default 100, env `MAX_MEGAPIXELS`) after reading only the header. Lock order is always file lock → decode slot. Thumbnail cache files are written atomically (`writeThumbs` → `writeThumb` → `writeFileAtomic`); a cache miss for either size generates both from one decode, deriving the small thumb from the medium one; zero-length cache files are treated as missing. ffmpeg calls run under a 30 s timeout (`runFFmpeg`).

**Malformed-file repair**: when the stdlib decoder fails, ffmpeg decodes instead; only `.jpg`/`.jpeg` originals are ever rewritten in place (`repairOriginalJPEG` returns `errNotJPEG` for anything else).

**Warmup strategy**: at startup, `warmupThumbnails` bounds itself with `semFull` (capacity 2, so one global decode slot stays free for interactive requests) and takes each file's mutex before decoding; `semLight` (capacity 8) handles files where only dimensions are missing (uses `image.DecodeConfig` to read just the header, ~1 KB).

**Watcher folder moves**: inotify reports a directory rename as a single event, so `evictFolder` purges DB rows, user data and cached thumbs under the old path. In-app moves (`handleMove`) re-key favorites/views via `moveUserData` *before* the rename so the watcher's delete of the old path finds nothing to drop.

**Folder rename** (`POST /api/rename-folder` `{path, name}`, name-only — the parent stays): `renameFolderRecords` re-keys `files` (path + folder) and favorites/views *before* `os.Rename`, then the `cache/s` and `cache/m` subtrees are renamed, so nothing is regenerated. The watcher's later events are harmless: `evictFolder(old)` finds no rows, and `indexArrivedFile` skips files whose DB mtime already matches. Top-level folders assigned in `config.yaml` can't be renamed (409) — the assignment matches by name, so a rename would make a private folder public. Frontend: pencil button in `FolderTree` (`editable` prop, side menu only) → `RenameFolderModal`; `App.vue` keeps a session list of renames and maps stale `/folder/...` history entries forward on popstate.

**Background uploads**: uploads go through an IndexedDB queue (`frontend/src/uploadQueue.js`, shared by page and SW) — job metadata in `jobs`, the file in `blobs` (separate because re-putting a record holding a Blob makes Chrome store another copy of the whole file). The page enqueues, registers the `uploads` Background Sync and posts `uploads-kick`; the SW uploads 1.5 MB chunks in `sync`/`message` events (150 s budget per event, then re-registers), so closing the app doesn't stop it, and shows a notification while no window is visible. A Web Lock (`navigator.locks`) keeps one runner at a time. Without Background Sync (Firefox, Safari) `composables/uploadRunner.js` runs the same engine in the page. `UploadDialog` is a view of the queue, and `UploadIndicator` is the header upload button: idle it opens the file picker and `App.startUpload` queues the files into the current folder (no chooser; the side menu's upload still asks for a folder), while uploads run or have failed it becomes a ring (byte progress, files left, red on failures) that reopens the dialog; queue changes made from the page go through `refreshUploads()` so both stay current; `App.vue` reopens it on launch for unfinished/failed jobs and toasts uploads that finished while it was hidden. Server side, a chunked upload's final chunk is idempotent: after saving, `cache/uploads/<id>/result.json` is kept (swept after 24 h) and a replayed final chunk gets it back.

**Date resolution priority**: EXIF `DateTimeOriginal` → filename pattern `YYYYMMDD_HHMMSS` at basename start → file mtime. Stored in `date_taken` column; skips re-extraction when mtime is unchanged.

**File identity**: the relative path from `photosDir` (forward-slash separated, e.g. `vacation/beach/photo.jpg`) is the stable key used in the database, API paths, and cache filenames.

### Frontend (`frontend/src/`)

Vue 3 + Vite 5 SPA. Three composables drive most logic:

- `useGallery.js` — fetches `/api/images`, manages sort/filter state, provides local image mutation helpers (used after crop/delete to update the in-memory list without a full refetch)
- `useVirtualScroll.js` — virtual row windowing: computes which rows are visible, sizes spacer divs to maintain correct scroll height
- `useYearScrollbar.js` — maps image index positions to year labels, tracks touch-drag state for the year handle
- `useFastScroll.js` — scroll-speed detector (pure `nextFastScrollState` + idle timer) driving `ScrollDateTooltip`, the month/year pill under the sticky header — the only date indicator, shown while flinging or dragging the year scrollbar in date-ordered views

### Build embedding

Production: `make build` copies `frontend/dist` into `cmd/server/frontend/dist/` before compiling the binary. The `embed.go` file (build tag `!dev`) embeds it with `//go:embed all:frontend/dist`. The dev build tag skips this and reads from disk.

## Testing

Tests use `setupTestEnv(t)` (defined in `handlers_test.go`) which:
- Creates isolated temp dirs for photos and cache
- Opens an in-memory SQLite database (`openDB(":memory:")`)
- Saves/restores all mutable globals on `t.Cleanup`

Each test file has a `_test.go` counterpart: `handlers_test.go`, `cache_test.go`, `thumbnails_test.go`, `validation_test.go`, `jpeg_test.go`, `meta_test.go`, `watcher_test.go`, `db_test.go`.
