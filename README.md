# Home Photo Frame

A self-hosted PWA photo gallery designed to run on a **custom digital photo frame** — a Raspberry Pi or similar SBC connected to a screen, showing a rotating slideshow of family photos. The same server also acts as the upload target: family members share photos directly from their phones into the frame via the Android share sheet.

Drop images into a folder, get a fast infinite-scroll gallery with full-screen lightbox, in-app cropping, per-file upload progress, and Android share-target support — all shipped as a single Go binary with the frontend embedded. The `/api/images` endpoint is intentionally simple so a custom slideshow client (e.g. a Python script or a second browser tab in kiosk mode) can consume it directly without parsing HTML.

> **No built-in authentication.** The app is designed to run behind a reverse proxy or identity-aware access layer — [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/), Tailscale, nginx with Basic Auth, Authelia, etc. Do not expose port 8080 directly to the internet.

## Stack

| Layer | Tech |
|---|---|
| Backend | Go 1.22 — `net/http`, `disintegration/imaging`, `fsnotify` |
| Frontend | Vue 3 · Vite 5 |
| PWA / SW | `vite-plugin-pwa` · Workbox (injectManifest) |
| Deployment | Single self-contained binary via `go:embed` |

## Features

- **Infinite scroll** — 150 images per page, prefetches before reaching the bottom
- **Thumbnail cache** — content-addressed JPEG thumbnails (400 px small, configurable medium); generated on demand and cached on disk; URL hash changes on file modification so browsers never show stale images
- **File watcher** — detects images added, edited, or removed externally (inotify) and keeps the in-memory index in sync without a restart
- **Nightly reconcile** — runs at 03:00 local time to catch any drift between disk, in-memory cache, and metadata (handles missed watcher events, external deletions, etc.)
- **Lightbox** — full-screen viewer, swipe/keyboard navigation, slide transitions
- **In-app crop** — drag-to-select crop UI; saves a new file, deletes the original
- **Delete** — two-tap confirmation in the lightbox
- **Upload** — drag-and-drop or file picker with per-file progress bar; optional post-upload crop queue
- **Sort** — by filename or photo date (EXIF → filename pattern → mtime), ascending or descending; persisted to localStorage
- **Share original** — Web Share API (file blob) on mobile; download fallback on desktop
- **Android share target** — share photos *into* the app from any Android app; the service worker intercepts the POST, stores files in the Cache API, the app shows per-file upload progress
- **PWA** — installable, offline shell via Workbox precaching
- **Multi-instance** — configurable title and PWA manifest name so two parallel instances (e.g. prod + test) appear as distinct installed apps

## Quickstart

### Prerequisites

- Go 1.22+ (`make install-go` will download it if missing)
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
└── ...
```

Supported formats: **JPEG, PNG, GIF, WebP**

### Production (single binary)

```bash
make build          # builds frontend, compiles binary with embedded assets
./photo-frame       # serves on :8080
```

**Options**

| Flag | Env var | Default | Description |
|---|---|---|---|
| `-photos` | `PHOTOS_DIR` | `./photos` | Directory containing source images |
| `-cache` | `CACHE_DIR` | `./cache` | Directory for thumbnail cache and metadata |
| `-port` | `PORT` | `8080` | Port to listen on (`8080` or `:8080`) |
| `-title` | `APP_TITLE` | `Photo Frame` | App title shown in the browser tab, header, and PWA manifest |
| `-medium-width` | `MEDIUM_WIDTH` | `2000` | Max pixel width for medium thumbnails used in the lightbox |
| `-bg-color` | `BG_COLOR` | `#0a0a0f` | Primary background hex colour; header and letterbox areas are derived from it automatically |
| `-icons-dir` | `ICONS_DIR` | *(embedded)* | Directory with custom `icon-192.png` / `icon-512.png`; falls back to built-in icons when unset |
| `-video` | `VIDEO` | `false` | Enable MP4 upload, video thumbnails, and in-browser playback. Requires `ffmpeg` in `PATH` (`VIDEO=1`) |

The binary is fully self-contained — copy it anywhere with a `photos/` folder alongside and it works.

### Docker

A multi-stage `Dockerfile` is included. It builds the Node frontend, compiles the Go binary with the frontend embedded, and produces a minimal Alpine runtime image (~30 MB including ffmpeg).

**Build and run**

```bash
docker build -t photo-frame .
docker run -p 8080:8080 \
  -v /path/to/photos:/data/photos \
  -v /path/to/cache:/data/cache \
  photo-frame
```

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
| `GET` | `/api/thumb/{hash}/{filename}` | 400 px thumbnail (JPEG, content-addressed, 1-year cache) |
| `GET` | `/api/thumb-medium/{hash}/{filename}` | Medium thumbnail up to `MEDIUM_WIDTH` px wide |
| `GET` | `/api/original/{filename}` | Original unmodified file |
| `POST` | `/api/upload` | Upload a single image (`multipart/form-data`, field `file`) |
| `POST` | `/api/crop/{filename}` | Crop image. Body: `{"x","y","width","height"}` in pixels |
| `DELETE` | `/api/delete/{filename}` | Delete image and its thumbnail cache |
| `GET/POST` | `/manifest.webmanifest` | PWA manifest with `name`/`short_name` injected from `-title` |

Thumbnail URLs are content-addressed: the hash component encodes the source filename and OS mtime. When a file is modified externally the hash changes, so browsers automatically fetch a fresh thumbnail while old ones remain permanently cacheable.

### GET /api/images

Returns a sorted list of all images (or a paginated page when `limit` is supplied). This is the primary endpoint for building a custom slideshow client.

**Query parameters**

| Parameter | Values | Default | Description |
|---|---|---|---|
| `sort` | `taken` \| `mtime` \| `name` | `taken` | Sort field. `taken` uses EXIF DateTimeOriginal → filename pattern `YYYYMMDD_HHMMSS` → file mtime. `mtime` sorts by OS modification time (useful to see recently added files first) |
| `order` | `asc` \| `desc` | `desc` | Sort direction |
| `limit` | 1–200 | *(omit for all)* | Images per page. When omitted all images are returned in one response |
| `page` | integer ≥ 1 | `1` | Page number (1-based); only meaningful when `limit` is set |

**Example requests**

```
GET /api/images?sort=date&order=desc          # all images
GET /api/images?sort=date&order=desc&limit=20&page=2  # page 2, 20 per page
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
      "thumbMedium": "/api/thumb-medium/a3f1c9d2e4b5a6c7/20240318_132033_holiday.jpg"
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
| `filename` | string | Filename within the photos directory; use as the path component for `/api/original/` and `/api/crop/` |
| `modTime` | RFC 3339 | Best available date (EXIF → filename → mtime). Use this for display and chronological ordering |
| `size` | integer | File size in bytes |
| `width`, `height` | integer | Original image dimensions in pixels (0 if not yet indexed) |
| `thumbSmall` | string | URL of the 400 px thumbnail; ready to use in an `<img src>` |
| `thumbMedium` | string | URL of the medium-res thumbnail (default 2000 px wide); suitable for a full-screen slideshow |
| `total` | integer | Total number of images across all pages |
| `page` | integer | Current page |
| `limit` | integer | Page size used for this response |

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

Install the PWA from Chrome on Android ("Add to Home Screen"). After installation the app appears in the Android share sheet for images.

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
│       │   └── useGallery.js pagination, sort state, local image mutations
│       └── components/
│           ├── GalleryGrid.vue       infinite-scroll grid with IntersectionObserver
│           ├── LightboxModal.vue     full-screen viewer, share/download, delete, crop
│           ├── ImageCropper.vue      drag-to-select crop editor
│           ├── UploadDialog.vue      per-file upload progress (picker / drag-drop)
│           ├── ShareUploader.vue     per-file upload progress (Android share target)
│           └── PostUploadCropQueue.vue  post-upload crop queue
├── photos/                   source images (not committed)
└── cache/                    thumbnail cache + meta.json (not committed)
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
