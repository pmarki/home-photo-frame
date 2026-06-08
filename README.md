# Home Photo Frame

A self-hosted PWA photo gallery designed to run on a **custom digital photo frame** — a Raspberry Pi or similar SBC connected to a screen, showing a rotating slideshow of family photos. The same server also acts as the upload target: family members share photos directly from their phones into the frame via the Android share sheet.

Drop images into a folder, get a fast infinite-scroll gallery with full-screen lightbox, in-app cropping, per-file upload progress, and Android share-target support — all shipped as a single Go binary with the frontend embedded. The `/api/images` endpoint is intentionally simple so a custom slideshow client (e.g. a Python script or a second browser tab in kiosk mode) can consume it directly without parsing HTML.

> **No built-in authentication.** The app is designed to run behind a reverse proxy or identity-aware access layer — [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/), Tailscale, nginx with Basic Auth, Authelia, etc. Do not expose port 8080 directly to the internet.

> The server sets `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`, and `Referrer-Policy: same-origin` on all responses, and enforces read/write/idle timeouts on HTTP connections.

## Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.25 — `net/http`, `disintegration/imaging`, `fsnotify`, `modernc.org/sqlite` |
| Frontend | Vue 3 · Vite 5 |
| PWA / SW | `vite-plugin-pwa` · Workbox (injectManifest) |
| Deployment | Single self-contained binary via `go:embed` |

## Features

- **Subfolder support** — photos can be organised in arbitrary subdirectories; each image is identified by its relative path (`vacation/2024/photo.jpg`), which is exposed in the API and displayed in the lightbox
- **Virtual scroll** — only the rows currently visible (plus a small buffer) are rendered; spacer divs hold the total height so the scrollbar stays accurate across tens of thousands of images
- **Lazy thumbnail loading** — thumbnails are requested 200 ms after they enter the viewport; scrolling away before the timer fires cancels the request entirely; images fade in on load so the broken-image icon never appears
- **Thumbnail cache** — JPEG thumbnails (400 px small, configurable medium) cached under `cache/s/` and `cache/m/`, mirroring the source directory structure; URL hash changes on file modification so browsers never show stale thumbnails or originals
- **Efficient warmup** — at startup, already-cached thumbnails use `image.DecodeConfig` (reads only the image header, ~1 KB) to recover stored dimensions instead of decoding the full image; full decodes are limited to 2 concurrent workers to bound peak memory; GC is tuned aggressively during warmup and heap is returned to the OS afterward
- **File watcher** — detects images added, edited, or removed externally (inotify) and keeps the database in sync without a restart; watches subdirectories recursively and picks up newly created directories automatically
- **Nightly reconcile** — runs at 03:00 local time to catch any drift between disk and database (handles missed watcher events, external deletions, etc.)
- **Lightbox** — full-screen viewer with slide transitions; double-click or double-tap to zoom to 2× centred on the tap point; drag to pan while zoomed; swipe-to-navigate disabled while zoomed
- **Lightbox footer** — shows the relative file path (e.g. `vacation/2024/photo.jpg`); click/tap to copy to clipboard
- **In-app crop** — drag-to-select crop UI; saves a new file, deletes the original
- **Delete** — two-tap confirmation in the lightbox
- **Upload** — drag-and-drop or file picker with per-file progress bar; chunked (1.5 MB/chunk) so uploads survive slow connections and proxy read-timeouts; 500 MB per-file cap enforced server-side; retry button for failed files; optional post-upload crop queue
- **Year scrollbar** — Google Photos-style overlay on the right edge: year markers fade in while scrolling (highlighted for the current year), with a touch-draggable 3-D handle for jumping directly to any year; hidden on mouse/desktop (labels only), touch-only on mobile
- **Sort** — by filename, photo date (EXIF → filename pattern → mtime), or modification time, ascending or descending; persisted to localStorage
- **Share original** — Web Share API (file blob) on mobile; download fallback on desktop
- **Android share target** — share photos *into* the app from any Android app; the service worker intercepts the POST, stores files in the Cache API, the app shows per-file upload progress
- **PWA** — installable, offline shell via Workbox precaching; `/api/config` (title, colours) is cached by the service worker and in `localStorage` so the app loads with the correct theme instantly, even offline
- **Multi-instance** — configurable title and PWA manifest name so two parallel instances (e.g. prod + test) appear as distinct installed apps

## Quickstart

### Prerequisites

- Go 1.25+ (`make install-go` will download it if missing)
- Node.js 18+ with npm

### First-time setup

```bash
git clone <repo> && cd home-photo-frame
make setup          # installs Go, npm deps, generates icons
```

### Drop in photos

```
photos/
├── IMG_0001.jpg
├── 20240318_132033_holiday.jpg   ← filename date used when no EXIF
├── vacation/
│   ├── IMG_0042.jpg
│   └── beach/
│       └── DSC_1234.jpg
└── ...
```

Subdirectories are indexed recursively. The relative path (e.g. `vacation/beach/DSC_1234.jpg`) is used as the stable identity for each image across all APIs.

Supported image formats: **JPEG, PNG, GIF, WebP**

Video formats (requires `-video` / `VIDEO=1`): **MP4, WebM, MOV, M4V**

### Production (single binary)

```bash
make build          # builds frontend, compiles binary with embedded assets
./photo-frame       # serves on :8080
```

**Options**

| Flag | Env var | Default | Description |
|---|---|---|---|
| `-photos` | `PHOTOS_DIR` | `./photos` | Directory containing source images |
| `-cache` | `CACHE_DIR` | `./cache` | Directory for thumbnail cache |
| `-db-dir` | `DB_DIR` | *(binary directory)* | Directory for the SQLite database file (`files.db`) |
| `-port` | `PORT` | `8080` | Port to listen on (`8080` or `:8080`) |
| `-title` | `APP_TITLE` | `Photo Frame` | App title shown in the browser tab, header, and PWA manifest |
| `-medium-width` | `MEDIUM_WIDTH` | `2000` | Max pixel width for medium thumbnails used in the lightbox |
| `-bg-color` | `BG_COLOR` | `#0a0a0f` | Primary background hex colour; header and letterbox areas are derived from it automatically |
| `-icons-dir` | `ICONS_DIR` | *(embedded)* | Directory with custom `icon-192.png` / `icon-512.png`; falls back to built-in icons when unset |
| `-video` | `VIDEO` | `false` | Enable video upload, thumbnails (via ffmpeg), and in-browser playback. Accepted formats: `.mp4`, `.webm`, `.mov`, `.m4v`. Requires `ffmpeg` in `PATH` (`VIDEO=1`) |

> **ffmpeg is also recommended even without `-video`.** Go's stdlib JPEG decoder is strict and rejects some real-world camera JPEGs (typical error: *missing 0xff00 sequence*). When `ffmpeg` is in `PATH`, the server transparently falls back to it for both warmup and on-demand thumbnail generation, so problematic files still display correctly. Without `ffmpeg` those specific files return `500` and are logged as `thumb: decode … missing 0xff00 sequence`.

The binary is fully self-contained — copy it anywhere with a `photos/` folder alongside and it works.

### Docker

A multi-stage `Dockerfile` is included. It builds the Node frontend, compiles the Go binary with the frontend embedded, and produces a minimal Alpine runtime image (~30 MB including ffmpeg).

**Build and run**

```bash
docker build -t photo-frame .
docker run -p 8080:8080 \
  -v /path/to/photos:/data/photos \
  -v /path/to/cache:/data/cache \
  -e DB_DIR=/data/cache \
  photo-frame
```

> **Note:** Set `DB_DIR` to a persistent volume path (e.g. the same as `CACHE_DIR`). By default the database is created next to the binary inside the container, which is lost on container restart.

**Docker Compose**

A fully-annotated `docker-compose.yml` is included with every env var and volume documented:

```bash
# copy and edit to taste
cp docker-compose.yml my-frame/docker-compose.yml

docker compose up -d
```

Minimal working compose:

```yaml
services:
  photo-frame:
    image: photo-frame:latest
    ports:
      - "8080:8080"
    environment:
      APP_TITLE: Living Room Frame
      BG_COLOR: "#0a0a1a"
      DB_DIR: /data/cache
    volumes:
      - /mnt/nas/photos:/data/photos
      - /mnt/nas/cache:/data/cache
    restart: unless-stopped
```

**Video support**

`ffmpeg` is already included in the image. Enable it with:

```yaml
environment:
  VIDEO: "1"
```

**Custom icons**

Mount a directory containing `icon-192.png` and `icon-512.png`:

```yaml
environment:
  ICONS_DIR: /data/icons
volumes:
  - /path/to/icons:/data/icons
```

### Running two instances in parallel

```bash
# Production
./photo-frame -port 8080 -photos ./photos -cache ./cache -title "Photo Frame"

# Test / staging
./photo-frame -port 8081 -photos ./photos-test -cache ./cache-test -title "Photo Frame DEV"
```

Both will appear as separate entries when installed as PWAs.

### Development

Two terminals:

```bash
# Terminal 1 — Go API server (reads frontend/dist from disk, no embed)
make backend-dev

# Terminal 2 — Vite dev server with HMR on :5173
make frontend-dev
```

Open **http://localhost:5173** — Vite proxies `/api` requests to the Go backend on `:8080`.

> `backend-dev` uses `-tags dev` which skips `go:embed` so you don't need a pre-built `frontend/dist` to start the server.

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/images` | Paginated image list (see below) |
| `GET` | `/api/config` | Runtime config: `{"title": "..."}` |
| `GET` | `/api/thumb/{hash}/{path}` | 400 px thumbnail (JPEG, content-addressed, 1-year cache) |
| `GET` | `/api/thumb-medium/{hash}/{path}` | Medium thumbnail up to `MEDIUM_WIDTH` px wide |
| `GET` | `/api/original/{hash}/{path}` | Original unmodified file (1-year immutable cache); falls back to `/api/original/{path}` with 1-hour cache for backward compat |
| `POST` | `/api/upload` | Upload an image — single-file or chunked (see below) |
| `POST` | `/api/crop/{path}` | Crop image. Body: `{"x","y","width","height"}` in pixels |
| `DELETE` | `/api/delete/{path}` | Delete image and its thumbnail cache |
| `GET/POST` | `/manifest.webmanifest` | PWA manifest with `name`/`short_name` injected from `-title` |

`{path}` is the relative path from the photos directory, e.g. `vacation/beach/photo.jpg` or just `photo.jpg` for root-level files. Each path component is percent-encoded separately so slashes remain real URL separators.

Thumbnail and original URLs are content-addressed: the hash component encodes the source path and OS mtime. When a file is modified externally the hash changes, so browsers automatically fetch fresh content while old responses remain permanently cacheable.

### GET /api/images

Returns a sorted list of all images (or a paginated page when `limit` is supplied). This is the primary endpoint for building a custom slideshow client.

**Query parameters**

| Parameter | Values | Default | Description |
|---|---|---|---|
| `sort` | `taken` \| `mtime` \| `name` | `taken` | Sort field. `taken` uses EXIF DateTimeOriginal → filename pattern `YYYYMMDD_HHMMSS` → file mtime. `mtime` sorts by OS modification time (useful to see recently added files first). `name` sorts by full relative path |
| `order` | `asc` \| `desc` | `desc` | Sort direction |
| `folder` | relative path | *(all)* | Limit results to a folder and all its subfolders. E.g. `folder=vacation` returns `vacation/a.jpg` and `vacation/hawaii/b.jpg`. Omit to return all files |
| `type` | `image` \| `video` | *(all)* | Filter by file type |
| `search` | string | *(none)* | Case-insensitive substring match on filename (not full path) |
| `limit` | 1–10 000 | *(omit for all)* | Images per page. When omitted all matching files are returned in one response |
| `page` | integer ≥ 1 | `1` | Page number (1-based); only meaningful when `limit` is set |

**Example requests**

```
GET /api/images?sort=taken&order=desc                        # all images
GET /api/images?folder=vacation&sort=taken&order=desc        # vacation folder + subfolders
GET /api/images?type=video                                   # videos only
GET /api/images?search=beach&limit=20&page=1                 # filename search
GET /api/images?sort=taken&order=desc&limit=20&page=2        # page 2, 20 per page
```

**Response** `200 OK` `application/json`

```json
{
  "images": [
    {
      "filename":    "20240318_132033_holiday.jpg",
      "modTime":     "2024-03-18T13:20:33Z",
      "size":        3142567,
      "width":       4032,
      "height":      3024,
      "thumbSmall":  "/api/thumb/a3f1c9d2e4b5a6c7/20240318_132033_holiday.jpg",
      "thumbMedium": "/api/thumb-medium/a3f1c9d2e4b5a6c7/20240318_132033_holiday.jpg",
      "original":    "/api/original/a3f1c9d2e4b5a6c7/20240318_132033_holiday.jpg"
    }
  ],
  "total": 142,
  "page":  1,
  "limit": 20
}
```

**Fields**

| Field | Type | Description |
|---|---|---|
| `filename` | string | Filename within the photos directory; use as the path component for `/api/crop/` and `/api/delete/` |
| `modTime` | RFC 3339 | Best available date (EXIF → filename → mtime). Use this for display and chronological ordering |
| `size` | integer | File size in bytes |
| `width`, `height` | integer | Original image dimensions in pixels (0 if not yet indexed) |
| `thumbSmall` | string | URL of the 400 px thumbnail; ready to use in an `<img src>` |
| `thumbMedium` | string | URL of the medium-res thumbnail (default 2000 px wide); suitable for a full-screen slideshow |
| `original` | string | Hash-based URL of the original file; immutable-cached, changes when the source file changes |
| `total` | integer | Total number of images across all pages |
| `page` | integer | Current page |
| `limit` | integer | Page size used for this response |

### POST /api/upload

Accepts `multipart/form-data`. Two modes:

**Single-file** (legacy, for small files or direct API use):
- `file` — the image blob

**Chunked** (used by the built-in upload UI — survives slow connections and proxy read-timeouts):
- `file` — one chunk blob (up to 1.5 MB recommended)
- `uploadId` — a UUID string that groups chunks into one upload session
- `chunkIndex` — 0-based chunk number
- `totalChunks` — total number of chunks for this file
- `filename` — original filename (used for extension validation and final assembly)

Intermediate chunks return `{"status":"chunk_ok"}`. The final chunk (and single-file uploads) return the same JSON:

```json
{
  "filename":    "photo.jpg",
  "thumbSmall":  "/api/thumb/a3f1c9d2e4b5a6c7/photo.jpg",
  "thumbMedium": "/api/thumb-medium/a3f1c9d2e4b5a6c7/photo.jpg",
  "original":    "/api/original/a3f1c9d2e4b5a6c7/photo.jpg"
}
```

Returns `409 Conflict` if a file with that name already exists. The total request body is capped at **500 MB**; exceeding it returns `413`.

**Slideshow recipe** — omit `limit` to get all images in one request, then cycle through `thumbMedium` URLs (or `original`) at whatever interval you like. Re-fetch nightly to pick up new photos.

```python
import requests, random, time

BASE = "http://photo-frame.local:8080"

r = requests.get(f"{BASE}/api/images", params={"sort": "date", "order": "desc"})
images = r.json()["images"]

random.shuffle(images)
for img in images:
    url = BASE + img["thumbMedium"]
    # display url on screen …
    time.sleep(30)
```

## PWA & Android Share Target

Install the PWA from Chrome on Android ("Add to Home Screen"). After installation the app appears in the Android share sheet for images and (when `-video` is enabled) videos.

**Share flow:**
1. User shares a photo from Gallery / Camera / any app → selects *Photo Frame*
2. Android POSTs the image to `/share-target`
3. The service worker intercepts the POST, stores files in the Cache API, and redirects to `/?share-pending=1` — no blank splash
4. The app opens, reads pending files from cache, and shows a per-file upload progress sheet
5. Files are uploaded to `/api/upload`; an optional crop queue opens on completion

> Share target requires the PWA to be **installed** and served over **HTTPS** in production.

## Project structure

```
/
├── main.go                   Go server (API handlers, warmup, file watcher, SPA routing)
├── embed.go                  go:embed frontend/dist  (production build tag)
├── embed_dev.go              filesystem fallback      (build tag: dev)
├── go.mod / go.sum
├── Makefile
├── Dockerfile                multi-stage build (Node → Go → Alpine runtime)
├── docker-compose.yml        annotated example with all env vars and volumes
├── cmd/
│   └── genicons/             generates PWA icon PNGs (stdlib only, no deps)
├── frontend/
│   ├── index.html
│   ├── vite.config.js
│   ├── package.json
│   ├── public/
│   │   └── icons/            PWA icons (generated by cmd/genicons)
│   └── src/
│       ├── main.js
│       ├── sw.js             service worker (Workbox injectManifest + share target)
│       ├── App.vue           root component, routing between gallery / upload / crop
│       ├── composables/
│       │   ├── useGallery.js         pagination, sort state, local image mutations
│       │   ├── useVirtualScroll.js   windowed rendering — visible rows, spacers, scroll metrics
│       │   └── useYearScrollbar.js   year-label positions, active year, touch-handle state
│       └── components/
│           ├── GalleryGrid.vue           infinite-scroll virtual grid
│           ├── YearScrollbar.vue         right-edge year overlay with touch-draggable handle
│           ├── LightboxModal.vue         full-screen viewer, share/download, delete, crop
│           ├── ImageCropper.vue          drag-to-select crop editor
│           ├── UploadDialog.vue          per-file upload progress (picker / drag-drop)
│           ├── ShareUploader.vue         per-file upload progress (Android share target)
│           ├── GalleryPlaceholder.vue    skeleton tile for not-yet-loaded images
│           └── PostUploadCropQueue.vue   post-upload crop queue
├── photos/                   source images (not committed)
├── cache/                    thumbnail cache: s/{path}, m/{path} (not committed)
└── files.db                  SQLite index (created next to binary; set DB_DIR to move it)
```

## Makefile targets

| Target | Description |
|---|---|
| `make setup` | Full first-time setup (Go + npm deps + icons) |
| `make build` | Production binary with embedded frontend |
| `make start` | Build then run |
| `make backend-dev` | Go server in dev mode (no embed) |
| `make frontend-dev` | Vite dev server with HMR |
| `make icons` | Regenerate PWA icons |
| `make deps` | `go mod tidy` |
| `make clean` | Remove dist, node_modules, cache, binary |
