// genicons generates PWA icon PNGs (192×192 and 512×512) using only
// the Go standard library. Run from the repo root:
//
//	go run ./cmd/genicons
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func main() {
	out := filepath.Join(repoRoot(), "frontend", "public", "icons")
	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, size := range []int{192, 512} {
		path := filepath.Join(out, fmt.Sprintf("icon-%d.png", size))
		if err := writeIcon(size, path); err != nil {
			log.Fatalf("icon %d: %v", size, err)
		}
		fmt.Printf("wrote %s\n", path)
	}
}

func rect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			img.Set(x, y, c)
		}
	}
}

func circle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
}

// triangle draws a filled isoceles triangle with apex at (ax, ay) pointing down to base y=by.
func triangle(img *image.RGBA, ax, ay, halfBase, by int, c color.RGBA) {
	for y := ay; y <= by; y++ {
		t := float64(y-ay) / float64(by-ay)
		hw := int(t * float64(halfBase))
		rect(img, ax-hw, y, ax+hw+1, y+1, c)
	}
}

func writeIcon(size int, path string) error {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// ── Background: warm gold (matches frame border) ─────────────────────
	bg := color.RGBA{R: 196, G: 158, B: 72, A: 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// ── Outer frame (warm gold) ───────────────────────────────────────────
	margin := size / 10
	ft := size / 9
	fx1, fy1 := margin, margin
	fx2, fy2 := size-margin, size-margin
	frameColor := color.RGBA{R: 196, G: 158, B: 72, A: 255}
	rect(img, fx1, fy1, fx2, fy2, frameColor)

	// ── Photo area (inside the frame) ───────────────────────────────────
	px1, py1 := fx1+ft, fy1+ft
	px2, py2 := fx2-ft, fy2-ft
	ph := py2 - py1
	pw := px2 - px1

	// Sky
	skyColor := color.RGBA{R: 135, G: 185, B: 215, A: 255}
	rect(img, px1, py1, px2, py2, skyColor)

	// Ground: bottom 38%
	groundY := py1 + ph*62/100
	groundColor := color.RGBA{R: 80, G: 130, B: 85, A: 255}
	rect(img, px1, groundY, px2, py2, groundColor)

	// Mountain: dark green triangle
	mountainColor := color.RGBA{R: 55, G: 100, B: 60, A: 255}
	apexX := px1 + pw/2
	apexY := py1 + ph*20/100
	triangle(img, apexX, apexY, pw*42/100, groundY, mountainColor)

	// Sun: upper right
	sunCx := px1 + pw*72/100
	sunCy := py1 + ph*22/100
	sunR := size / 16
	sunColor := color.RGBA{R: 250, G: 210, B: 60, A: 255}
	circle(img, sunCx, sunCy, sunR, sunColor)

	// ── Frame corner accents (darker gold squares) ───────────────────────
	ac := ft / 2
	accentColor := color.RGBA{R: 150, G: 115, B: 40, A: 255}
	rect(img, fx1, fy1, fx1+ac, fy1+ac, accentColor)
	rect(img, fx2-ac, fy1, fx2, fy1+ac, accentColor)
	rect(img, fx1, fy2-ac, fx1+ac, fy2, accentColor)
	rect(img, fx2-ac, fy2-ac, fx2, fy2, accentColor)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
