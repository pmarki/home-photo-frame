package main

import (
	"context"
	"errors"
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
	dbDir        string
	serverPort   string
	mediumWidth  int
	appTitle     string
	videoEnabled bool
	bgColor      string
	iconsDir     string
)

var frontendFS fs.FS
var manifestBody []byte
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

	// Default DB directory: same directory as the running binary.
	exePath, _ := os.Executable()
	defaultDBDir := filepath.Dir(exePath)
	flag.StringVar(&dbDir, "db-dir", envOr("DB_DIR", defaultDBDir), "directory for the SQLite database file (env: DB_DIR)")

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

	for _, p := range []*string{&photosDir, &cacheDir, &dbDir} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			log.Fatalf("cannot resolve path %s: %v", *p, err)
		}
		*p = abs
	}

	for _, dir := range []string{cacheDir, photosDir, dbDir, filepath.Join(cacheDir, "s"), filepath.Join(cacheDir, "m")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("cannot create directory %s: %v", dir, err)
		}
	}

	// db and cache dirs must be writable — SQLite writes the .db file and we
	// continuously write thumbs into cache/s and cache/m. photos dir is allowed
	// to be read-only (the repair-on-fallback path handles that gracefully).
	for _, c := range []struct{ dir, label string }{
		{dbDir, "db"},
		{cacheDir, "cache"},
	} {
		if err := assertDirWritable(c.dir); err != nil {
			log.Fatalf("%s directory %s is not writable: %v", c.label, c.dir, err)
		}
	}

	log.Printf("photos : %s", photosDir)
	log.Printf("cache  : %s", cacheDir)
	log.Printf("db     : %s", filepath.Join(dbDir, "files.db"))
	log.Printf("title  : %s", appTitle)

	if frontendFS != nil {
		if data, err := fs.ReadFile(frontendFS, "manifest.webmanifest"); err == nil {
			if body, berr := buildManifest(data); berr == nil {
				manifestBody = body
			} else {
				log.Printf("manifest: build failed: %v", berr)
			}
		} else {
			log.Printf("manifest: could not read: %v", err)
		}
	}

	var err error
	db, err = openDB(dbDir)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	syncFilesToDB(nil)

	safeGo("warmup", warmupThumbnails)
	go sweepOrphanedUploads()
	watchPhotosDir()
	scheduleNightlyReconcile()

	log.Printf("medium-width: %dpx", mediumWidth)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/images", handleImages)
	mux.HandleFunc("/api/folders", handleFolders)
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
	shutdownDone := make(chan struct{})
	go func() {
		<-quit
		log.Println("shutdown: signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown: server: %v", err)
		}
		if err := db.Close(); err != nil {
			log.Printf("shutdown: db: %v", err)
		}
		close(shutdownDone)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
	<-shutdownDone
	log.Println("shutdown: clean exit")
}
