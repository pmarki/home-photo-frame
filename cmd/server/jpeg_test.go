package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildTIFFLE builds a minimal little-endian TIFF containing a single
// Orientation tag and a non-zero IFD1 next-pointer.
func buildTIFFLE(orientation uint16) []byte {
	tiff := []byte{
		'I', 'I',                   // little-endian
		0x2A, 0x00,                  // magic 42
		0x08, 0x00, 0x00, 0x00,      // IFD0 at offset 8
		0x01, 0x00,                  // 1 IFD entry
		0x12, 0x01,                  // tag: Orientation (0x0112)
		0x03, 0x00,                  // type: SHORT
		0x01, 0x00, 0x00, 0x00,      // count: 1
		byte(orientation), byte(orientation >> 8), 0x00, 0x00, // value
		0x04, 0x00, 0x00, 0x00,      // IFD1 next-pointer (non-zero to test zeroing)
	}
	return tiff
}

// buildAPP1 wraps a TIFF payload in an EXIF APP1 marker segment.
func buildAPP1(tiff []byte) []byte {
	header := []byte{'E', 'x', 'i', 'f', 0, 0}
	payload := append(header, tiff...)
	segLen := uint16(len(payload) + 2)
	seg := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	return append(seg, payload...)
}

// buildJPEG wraps segments in a minimal JPEG (SOI … EOI).
func buildJPEG(segments ...[]byte) []byte {
	out := []byte{0xFF, 0xD8} // SOI
	for _, s := range segments {
		out = append(out, s...)
	}
	out = append(out, 0xFF, 0xD9) // EOI
	return out
}

// ── extractJPEGApp1 ───────────────────────────────────────────────────────

func TestExtractJPEGApp1(t *testing.T) {
	app1 := buildAPP1(buildTIFFLE(1))
	jpegWithApp1 := buildJPEG(app1)

	// Build a JPEG with an APP0 (JFIF) segment instead of APP1.
	jfifPayload := []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
	jfifSegLen := uint16(len(jfifPayload) + 2)
	app0 := []byte{0xFF, 0xE0, byte(jfifSegLen >> 8), byte(jfifSegLen)}
	app0 = append(app0, jfifPayload...)
	jpegWithApp0 := buildJPEG(app0)

	t.Run("returns APP1 from JPEG with EXIF", func(t *testing.T) {
		got := extractJPEGApp1(jpegWithApp1)
		if got == nil {
			t.Fatal("got nil, want APP1 bytes")
		}
		if !bytes.Equal(got, app1) {
			t.Errorf("got %x, want %x", got, app1)
		}
	})

	t.Run("returns nil for JPEG without EXIF APP1", func(t *testing.T) {
		if got := extractJPEGApp1(jpegWithApp0); got != nil {
			t.Errorf("got %x, want nil", got)
		}
	})

	t.Run("returns nil for non-JPEG data", func(t *testing.T) {
		if got := extractJPEGApp1([]byte("not a jpeg")); got != nil {
			t.Errorf("got %x, want nil", got)
		}
	})

	t.Run("returns nil for empty input", func(t *testing.T) {
		if got := extractJPEGApp1(nil); got != nil {
			t.Errorf("got %x, want nil", got)
		}
	})

	t.Run("returns nil for truncated JPEG", func(t *testing.T) {
		if got := extractJPEGApp1(jpegWithApp1[:5]); got != nil {
			t.Errorf("got %x, want nil", got)
		}
	})
}

// ── injectJPEGApp1 ────────────────────────────────────────────────────────

func TestInjectJPEGApp1(t *testing.T) {
	dst := []byte{0xFF, 0xD8, 0xAA, 0xBB, 0xCC}
	app1 := []byte{0xEE, 0xFF}

	t.Run("inserts app1 after SOI", func(t *testing.T) {
		got := injectJPEGApp1(dst, app1)
		want := []byte{0xFF, 0xD8, 0xEE, 0xFF, 0xAA, 0xBB, 0xCC}
		if !bytes.Equal(got, want) {
			t.Errorf("got %x, want %x", got, want)
		}
	})

	t.Run("does not modify original slices", func(t *testing.T) {
		origDst := make([]byte, len(dst))
		copy(origDst, dst)
		injectJPEGApp1(dst, app1)
		if !bytes.Equal(dst, origDst) {
			t.Error("dst was modified in place")
		}
	})

	t.Run("returns unchanged input when dst is too short", func(t *testing.T) {
		short := []byte{0xFF}
		got := injectJPEGApp1(short, app1)
		if !bytes.Equal(got, short) {
			t.Errorf("got %x, want %x", got, short)
		}
	})
}

// ── resetExifOrientation ──────────────────────────────────────────────────

func TestResetExifOrientation(t *testing.T) {
	t.Run("sets orientation to 1", func(t *testing.T) {
		app1 := buildAPP1(buildTIFFLE(8)) // 8 = rotate 270°
		result := resetExifOrientation(app1)

		// Orientation tag value is at TIFF offset 18 from the start of TIFF data.
		// TIFF data starts at app1[10] (after FF E1 + length + "Exif\0\0").
		tiff := result[10:]
		ifd0 := int(binary.LittleEndian.Uint32(tiff[4:8])) // = 8
		// First entry starts at ifd0+2; orientation value is at entry offset +8.
		valOff := ifd0 + 2 + 8
		got := binary.LittleEndian.Uint16(tiff[valOff:])
		if got != 1 {
			t.Errorf("orientation = %d, want 1", got)
		}
	})

	t.Run("zeroes IFD1 next-pointer", func(t *testing.T) {
		app1 := buildAPP1(buildTIFFLE(6))
		result := resetExifOrientation(app1)

		tiff := result[10:]
		ifd0 := int(binary.LittleEndian.Uint32(tiff[4:8]))
		n := int(binary.LittleEndian.Uint16(tiff[ifd0:]))
		nextPtr := ifd0 + 2 + n*12
		got := binary.LittleEndian.Uint32(tiff[nextPtr:])
		if got != 0 {
			t.Errorf("IFD1 next-pointer = %d, want 0", got)
		}
	})

	t.Run("does not modify other bytes", func(t *testing.T) {
		app1 := buildAPP1(buildTIFFLE(1))
		result := resetExifOrientation(app1)
		// With orientation already 1 and IFD1 pointer zeroed, the "Exif\0\0"
		// header must be intact.
		if string(result[4:10]) != "Exif\x00\x00" {
			t.Errorf("EXIF header corrupted: %x", result[4:10])
		}
	})

	t.Run("returns input unchanged when too short", func(t *testing.T) {
		short := []byte{0xFF, 0xE1, 0x00, 0x08}
		got := resetExifOrientation(short)
		if !bytes.Equal(got, short) {
			t.Errorf("short input was not returned unchanged")
		}
	})

	t.Run("returns input unchanged for unknown byte order", func(t *testing.T) {
		// Build an APP1 with invalid byte-order marker.
		tiff := make([]byte, 20)
		copy(tiff, []byte{'X', 'X', 0x2A, 0x00})
		app1 := buildAPP1(tiff)
		got := resetExifOrientation(app1)
		if !bytes.Equal(got, app1) {
			t.Error("invalid byte-order input was modified")
		}
	})
}

// ── round-trip ────────────────────────────────────────────────────────────

func TestJPEGRoundTrip(t *testing.T) {
	// Build a JPEG, extract the APP1, reset orientation, re-inject, extract again.
	app1 := buildAPP1(buildTIFFLE(6))
	jpeg := buildJPEG(app1)

	extracted := extractJPEGApp1(jpeg)
	if extracted == nil {
		t.Fatal("extractJPEGApp1 returned nil")
	}

	reset := resetExifOrientation(extracted)
	newJPEG := injectJPEGApp1([]byte{0xFF, 0xD8, 0xFF, 0xD9}, reset)

	got := extractJPEGApp1(newJPEG)
	if got == nil {
		t.Fatal("extractJPEGApp1 returned nil after re-injection")
	}
	if !bytes.Equal(got, reset) {
		t.Errorf("re-extracted APP1 differs from reset APP1")
	}
}
