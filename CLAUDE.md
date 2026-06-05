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
| `thumbnails.go` | `thumbHash`, `thumbURLs`, `serveCachedThumb`, `warmupThumbnails`, per-file `fileMu` |
| `watcher.go` | `watchPhotosDir` (fsnotify), `reconcile`, `scheduleNightlyReconcile` (03:00 daily) |
| `meta.go` | `extractBestDate` (EXIF → filename pattern → mtime), `indexFileRecord` |
| `jpeg.go` | Raw JPEG EXIF manipulation: extract/inject APP1, reset IFD0 orientation + zero IFD1 |
| `validation.go` | `isValidPath`, `isValidFilename`, `encodePathSegments`, `extractVideoFrame` (ffmpeg) |
| `middleware.go` | `recoveryMiddleware`, `corsMiddleware`, `safeGo`, `safeLoop` |
| `embed.go` | `//go:embed all:frontend/dist` (production, build tag `!dev`) |
| `embed_dev.go` | `os.DirFS("./frontend/dist")` fallback (build tag `dev`) |

### Key design decisions

**SQLite concurrency**: `db.SetMaxOpenConns(1)` serializes all writes through Go's pool mutex, preventing `SQLITE_BUSY`. WAL mode is enabled for read concurrency.

**Content-addressed URLs**: `thumbHash(path, mtime)` = first 8 bytes of SHA256(path + "\x00" + mtime_ns). URLs are `immutable`-cached for 1 year; the hash changes only when the source file's mtime changes, so browsers auto-fetch fresh content.

**Thumbnail cache layout**: `cache/s/{relative_path}` (400 px small) and `cache/m/{relative_path}` (up to `mediumWidth` px). Cache paths mirror the source directory structure using the original filename.

**Per-file mutex** (`fileMu sync.Map`): crop and thumbnail generation for the same file share a single `*sync.Mutex` stored in a sync.Map, preventing concurrent writes to the same cache file.

**Crop flow**: reads original → `imaging.AutoOrientation` decodes → crop → re-encode → strip IFD1 thumbnail from EXIF APP1 and reset IFD0 orientation to 1 (since pixels already have rotation baked in) → atomic write via `.tmp` rename → delete stale cache files → re-index.

**Warmup strategy**: at startup, `warmupThumbnails` uses two semaphores — `semFull` (capacity 2) for full image decodes and `semLight` (capacity 8) for files where only dimensions are missing (uses `image.DecodeConfig` to read just the header, ~1 KB).

**Date resolution priority**: EXIF `DateTimeOriginal` → filename pattern `YYYYMMDD_HHMMSS` at basename start → file mtime. Stored in `date_taken` column; skips re-extraction when mtime is unchanged.

**File identity**: the relative path from `photosDir` (forward-slash separated, e.g. `vacation/beach/photo.jpg`) is the stable key used in the database, API paths, and cache filenames.

### Frontend (`frontend/src/`)

Vue 3 + Vite 5 SPA. Three composables drive most logic:

- `useGallery.js` — fetches `/api/images`, manages sort/filter state, provides local image mutation helpers (used after crop/delete to update the in-memory list without a full refetch)
- `useVirtualScroll.js` — virtual row windowing: computes which rows are visible, sizes spacer divs to maintain correct scroll height
- `useYearScrollbar.js` — maps image index positions to year labels, tracks touch-drag state for the year handle

### Build embedding

Production: `make build` copies `frontend/dist` into `cmd/server/frontend/dist/` before compiling the binary. The `embed.go` file (build tag `!dev`) embeds it with `//go:embed all:frontend/dist`. The dev build tag skips this and reads from disk.

## Testing

Tests use `setupTestEnv(t)` (defined in `handlers_test.go`) which:
- Creates isolated temp dirs for photos and cache
- Opens an in-memory SQLite database (`openDB(":memory:")`)
- Saves/restores all mutable globals on `t.Cleanup`

Each test file has a `_test.go` counterpart: `handlers_test.go`, `cache_test.go`, `thumbnails_test.go`, `validation_test.go`, `jpeg_test.go`, `meta_test.go`.
