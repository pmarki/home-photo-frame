package main

import (
	"flag"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/image/webp"
)

var (
	photosDir    string
	cacheDir     string
	serverPort   string
	mediumWidth  int
	appTitle     string
	videoEnabled bool
	bgColor      string
	iconsDir     string
)

// frontendFS is set by embed.go / embed_dev.go before main() runs.
var frontendFS fs.FS

// rawManifest holds the built manifest.webmanifest bytes, used as a template
// for dynamic title injection. Populated once in main() after flag parsing.
var rawManifest []byte

// frontendHandler is provided by either embed.go (production) or embed_dev.go (dev build tag).
// It returns an http.Handler that serves the compiled Vue frontend.
var frontendHandler func() http.Handler

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	flag.StringVar(&photosDir, "photos", envOr("PHOTOS_DIR", "./photos"), "directory containing source images (env: PHOTOS_DIR)")
	flag.StringVar(&cacheDir, "cache", envOr("CACHE_DIR", "./cache"), "directory for thumbnail cache (env: CACHE_DIR)")
	flag.StringVar(&serverPort, "port", envOr("PORT", "8080"), "port to listen on (env: PORT)")
	flag.StringVar(&appTitle, "title", envOr("APP_TITLE", "Photo Frame"), "app title shown in the browser tab and header (env: APP_TITLE)")
	mediumWidthDefault := 2000
	if v, err := strconv.Atoi(envOr("MEDIUM_WIDTH", "")); err == nil && v > 0 {
		mediumWidthDefault = v
	}
	flag.IntVar(&mediumWidth, "medium-width", mediumWidthDefault, "max pixel width for medium thumbnails (env: MEDIUM_WIDTH)")
	videoDefault := strings.EqualFold(envOr("VIDEO", ""), "1") || strings.EqualFold(envOr("VIDEO", ""), "true")
	flag.BoolVar(&videoEnabled, "video", videoDefault, "enable mp4 video upload and thumbnails; requires ffmpeg (env: VIDEO=1)")
	flag.StringVar(&bgColor, "bg-color", envOr("BG_COLOR", "#0a0a0f"), "primary background hex colour (env: BG_COLOR)")
	flag.StringVar(&iconsDir, "icons-dir", envOr("ICONS_DIR", ""), "directory with custom icon files; falls back to embedded (env: ICONS_DIR)")
	flag.Parse()

	if !isValidHexColor(bgColor) {
		log.Printf("warning: invalid BG_COLOR %q, falling back to #0a0a0f", bgColor)
		bgColor = "#0a0a0f"
	}

	if videoEnabled {
		for ext, mt := range map[string]string{
			".mp4":  "video/mp4",
			".webm": "video/webm",
			".mov":  "video/quicktime",
			".m4v":  "video/mp4",
		} {
			supportedExts[ext] = true
			mime.AddExtensionType(ext, mt)
		}
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			log.Println("warning: ffmpeg not found in PATH — video thumbnails will fail")
		}
	}

	for _, p := range []*string{&photosDir, &cacheDir} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			log.Fatalf("cannot resolve path %s: %v", *p, err)
		}
		*p = abs
	}

	for _, dir := range []string{cacheDir, photosDir, filepath.Join(cacheDir, "s"), filepath.Join(cacheDir, "m")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("cannot create directory %s: %v", dir, err)
		}
	}

	log.Printf("photos : %s", photosDir)
	log.Printf("cache  : %s", cacheDir)
	log.Printf("title  : %s", appTitle)

	// Read the built manifest once so handleManifest can inject the title.
	if frontendFS != nil {
		if data, err := fs.ReadFile(frontendFS, "manifest.webmanifest"); err == nil {
			rawManifest = data
		} else {
			log.Printf("manifest: could not read: %v", err)
		}
	}

	metaPath = filepath.Join(cacheDir, "meta.json")
	loadMetaIndex()
	safeLoop("meta-saver", runMetaSaver)

	// Build the initial image cache synchronously so the gallery is available
	// immediately; warmup will update dimensions incrementally as it runs.
	buildImagesCache()

	safeGo("warmup", warmupThumbnails)
	go sweepOrphanedUploads()
	watchPhotosDir()
	scheduleNightlyReconcile()

	log.Printf("medium-width: %dpx", mediumWidth)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/images", handleImages)
	mux.HandleFunc("/api/config", handleConfig)
	mux.HandleFunc("/api/thumb/", handleThumb)
	mux.HandleFunc("/api/thumb-medium/", handleThumbMedium)
	mux.HandleFunc("/api/original/", handleOriginal)
	mux.HandleFunc("/api/upload", handleUpload)
	mux.HandleFunc("/api/crop/", handleCrop)
	mux.HandleFunc("/api/delete/", handleDelete)
	mux.HandleFunc("/manifest.webmanifest", handleManifest)
	mux.HandleFunc("/icons/", handleIcons)
	mux.Handle("/", frontendHandler())

	addr := serverPort
	if !strings.HasPrefix(addr, ":") {
		addr = ":" + addr
	}
	log.Printf("listening on %s", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      corsMiddleware(recoveryMiddleware(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Exiting...")
		os.Exit(0)
	}()

	log.Fatal(srv.ListenAndServe())
}
