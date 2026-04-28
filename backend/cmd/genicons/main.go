// genicons generates minimal PWA icon PNGs (192×192 and 512×512) using only
// the Go standard library. Run from the repo root:
//
//	cd /var/www/home-photo-frame && go run ./backend/cmd/genicons
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
	// __FILE__ is backend/cmd/genicons/main.go → go up 3 levels
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
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

func writeIcon(size int, path string) error {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Background: dark indigo #1a1a2e
	bg := color.RGBA{R: 26, G: 26, B: 46, A: 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// Draw a simple white camera-body rectangle in the centre.
	cx, cy := size/2, size/2
	bw, bh := size*2/5, size*3/10
	for y := cy - bh/2; y <= cy+bh/2; y++ {
		for x := cx - bw/2; x <= cx+bw/2; x++ {
			img.Set(x, y, color.RGBA{R: 220, G: 220, B: 240, A: 255})
		}
	}
	// Lens circle (dark)
	r := size / 8
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				img.Set(cx+x, cy+y, color.RGBA{R: 26, G: 26, B: 46, A: 255})
			}
		}
	}
	// Inner lens highlight
	r2 := size / 14
	for y := -r2; y <= r2; y++ {
		for x := -r2; x <= r2; x++ {
			if x*x+y*y <= r2*r2 {
				img.Set(cx+x, cy+y, color.RGBA{R: 100, G: 140, B: 220, A: 255})
			}
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
