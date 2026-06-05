package main

import (
	"bytes"
	"fmt"
	"image"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

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
