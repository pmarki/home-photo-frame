package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// errReadOnlyPhotos is returned by repairOriginalJPEG when the photos
// directory (or the file itself) can't be written. Callers should treat this
// as "skipped, not failed" so the log isn't noisy on read-only deployments.
var errReadOnlyPhotos = errors.New("photos dir is read-only")

// errNotJPEG is returned by repairOriginalJPEG for non-JPEG sources, which
// are never rewritten.
var errNotJPEG = errors.New("not a JPEG; original left untouched")

var videoExts = map[string]bool{
	".mp4":  true,
	".webm": true,
	".mov":  true,
	".m4v":  true,
}

var supportedExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
}

// isSupportedMedia reports whether filename has an extension this server
// indexes and serves (images, plus videos when enabled).
func isSupportedMedia(filename string) bool {
	return supportedExts[strings.ToLower(filepath.Ext(filename))]
}

func isVideo(filename string) bool {
	return videoExts[strings.ToLower(filepath.Ext(filename))]
}

func isValidHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ffmpegTimeout bounds every ffmpeg invocation. A pathological input can make
// ffmpeg hang indefinitely, and callers hold the per-file mutex and a decode
// slot while waiting.
const ffmpegTimeout = 30 * time.Second

// runFFmpeg runs ffmpeg with args under ffmpegTimeout and returns its stdout.
func runFFmpeg(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg", args...).Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ffmpeg: timed out after %s", ffmpegTimeout)
	}
	return out, err
}

// decodeBytesLimited decodes an encoded image after checking its header
// dimensions against the pixel limit.
func decodeBytesLimited(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if err := checkPixelLimit(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// extractVideoFrame runs ffmpeg to extract a single frame (5th frame, falling
// back to frame 0 for very short clips) and returns it as a decoded image.
func extractVideoFrame(srcPath string) (image.Image, error) {
	for _, filter := range []string{"select=gte(n\\,4)", "select=eq(n\\,0)"} {
		out, err := runFFmpeg(
			"-loglevel", "error",
			"-i", srcPath,
			"-vf", filter, "-vframes", "1",
			"-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "2", "-")
		if err == nil && len(out) > 0 {
			img, err := decodeBytesLimited(out)
			if err == nil {
				return img, nil
			}
			if errors.Is(err, errImageTooLarge) {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("ffmpeg: could not extract frame from %s", srcPath)
}

// decodeJPEGFallback re-decodes a problematic JPEG via ffmpeg, which is far
// more lenient than Go's stdlib image/jpeg (which rejects many real-world
// camera JPEGs with errors like "missing 0xff00 sequence"). Requires ffmpeg
// in PATH; returns an error if missing or if ffmpeg can't read the file.
func decodeJPEGFallback(srcPath string) (image.Image, error) {
	out, err := runFFmpeg(
		"-loglevel", "error",
		"-i", srcPath,
		"-frames:v", "1",
		"-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "2", "-")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ffmpeg: empty output")
	}
	img, err := decodeBytesLimited(out)
	if err != nil {
		return nil, fmt.Errorf("decode ffmpeg output: %w", err)
	}
	return img, nil
}

// isJPEGFile reports whether filename has a JPEG extension.
func isJPEGFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".jpg" || ext == ".jpeg"
}

// repairOriginalJPEG re-encodes a malformed JPEG (one that needed
// decodeJPEGFallback) over its source path so /api/original/... serves bytes
// the browser can render. Preserves the original APP1 EXIF segment when
// extractable (with orientation reset to 1 and IFD1 thumbnail stripped, since
// pixels are already auto-oriented); otherwise drops EXIF. img must be the
// already-decoded, auto-oriented pixels (i.e. the value returned by the
// fallback). Refuses non-JPEG sources: writing JPEG bytes over a PNG/GIF/WebP
// would destroy transparency and lossless data. Writes atomically. Returns
// errReadOnlyPhotos when the photos directory can't be written, so callers
// can downgrade the log.
func repairOriginalJPEG(srcPath string, img image.Image) error {
	if !isJPEGFile(srcPath) {
		return errNotJPEG
	}
	// Pre-flight: rename requires write on the parent directory and .tmp
	// creation needs the same. Probe with a short-lived file so we don't
	// burn a JPEG encode on a read-only deployment.
	probe, err := os.CreateTemp(filepath.Dir(srcPath), ".repair-probe-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return errReadOnlyPhotos
		}
		return fmt.Errorf("probe: %w", err)
	}
	probe.Close()
	os.Remove(probe.Name())

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	out := buf.Bytes()
	if orig, err := os.ReadFile(srcPath); err == nil {
		if app1 := extractJPEGApp1(orig); app1 != nil {
			out = injectJPEGApp1(out, resetExifOrientation(app1))
		}
	}
	if err := writeFileAtomic(srcPath, out, 0o644, true); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return errReadOnlyPhotos
		}
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// Temp files from writeFileAtomic are named tempPrefix + random + tempSuffix.
const (
	tempPrefix = ".pf-"
	tempSuffix = ".tmp"
)

// tempFileMaxAge is how old a temp file must be before sweepTempFiles removes
// it. Writes take seconds at most, so anything older was orphaned by a crash
// or power loss — the age guard keeps the sweep from racing a live write.
const tempFileMaxAge = time.Hour

// isOrphanCandidate reports whether name is a temp file this server creates:
// writeFileAtomic's ".pf-*.tmp", or the "<photo>.<ext>.tmp" names that older
// versions used for crops and repairs.
func isOrphanCandidate(name string) bool {
	if !strings.HasSuffix(name, tempSuffix) {
		return false
	}
	return strings.HasPrefix(name, tempPrefix) || isSupportedMedia(strings.TrimSuffix(name, tempSuffix))
}

// sweepTempFiles removes orphaned temp files older than tempFileMaxAge
// anywhere under root, returning how many were removed.
func sweepTempFiles(root string) int {
	cutoff := time.Now().Add(-tempFileMaxAge)
	removed := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil || !d.Type().IsRegular() || !isOrphanCandidate(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cutoff) {
			if err := os.Remove(p); err == nil {
				removed++
			} else {
				log.Printf("sweep: remove %s: %v", p, err)
			}
		}
		return nil
	})
	if removed > 0 {
		log.Printf("sweep: removed %d orphaned temp file(s) under %s", removed, root)
	}
	return removed
}

// writeFileAtomic writes data to a temp file in path's directory and renames
// it over path, so readers never observe a partially written file. The temp
// name ends in .tmp, which the watcher and directory walks ignore. When
// durable is true the data is fsynced before the rename (use for originals;
// thumbnails are regenerable and skip the cost).
func writeFileAtomic(path string, data []byte, perm os.FileMode, durable bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), tempPrefix+"*"+tempSuffix)
	if err != nil {
		return err
	}
	tmp := f.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if durable {
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// assertDirWritable verifies the process can create files in dir by writing
// (and then removing) a short-lived probe file. Detects read-only mounts,
// permission mismatches, and full filesystems before the server starts
// serving.
func assertDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".perm-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// isValidFilename rejects empty names, names containing path separators, and "..".
func isValidFilename(filename string) bool {
	return filename != "" && !strings.ContainsAny(filename, "/\\") && filename != ".." && filename != "."
}

// isValidPath accepts a forward-slash-separated relative path where every
// component passes isValidFilename. Root-level files ("photo.jpg") are valid;
// so are nested paths ("vacation/photo.jpg"). Leading/trailing slashes and ".."
// traversal are rejected.
func isValidPath(p string) bool {
	if p == "" || path.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if !isValidFilename(part) {
			return false
		}
	}
	return true
}

// encodePathSegments percent-encodes each "/" -separated path component so that
// slashes in the URL are real separators and special characters within each
// component are safely escaped.
func encodePathSegments(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
