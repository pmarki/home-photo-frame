package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// errReadOnlyPhotos is returned by repairOriginalJPEG when the photos
// directory (or the file itself) can't be written. Callers should treat this
// as "skipped, not failed" so the log isn't noisy on read-only deployments.
var errReadOnlyPhotos = errors.New("photos dir is read-only")

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

// extractVideoFrame runs ffmpeg to extract a single frame (5th frame, falling
// back to frame 0 for very short clips) and returns it as a decoded image.
func extractVideoFrame(srcPath string) (image.Image, error) {
	for _, filter := range []string{"select=gte(n\\,4)", "select=eq(n\\,0)"} {
		cmd := exec.Command("ffmpeg",
			"-loglevel", "error",
			"-i", srcPath,
			"-vf", filter, "-vframes", "1",
			"-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "2", "-")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			if img, _, err := image.Decode(bytes.NewReader(out)); err == nil {
				return img, nil
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
	cmd := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-i", srcPath,
		"-frames:v", "1",
		"-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "2", "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ffmpeg: empty output")
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("decode ffmpeg output: %w", err)
	}
	return img, nil
}

// repairOriginalJPEG re-encodes a malformed JPEG (one that needed
// decodeJPEGFallback) over its source path so /api/original/... serves bytes
// the browser can render. Preserves the original APP1 EXIF segment when
// extractable (with orientation reset to 1 and IFD1 thumbnail stripped, since
// pixels are already auto-oriented); otherwise drops EXIF. img must be the
// already-decoded, auto-oriented pixels (i.e. the value returned by the
// fallback). Uses .tmp + rename for atomicity. Returns errReadOnlyPhotos when
// the photos directory can't be written, so callers can downgrade the log.
func repairOriginalJPEG(srcPath string, img image.Image) error {
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
	tmp := srcPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return errReadOnlyPhotos
		}
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, srcPath); err != nil {
		os.Remove(tmp)
		if errors.Is(err, fs.ErrPermission) {
			return errReadOnlyPhotos
		}
		return fmt.Errorf("rename: %w", err)
	}
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
