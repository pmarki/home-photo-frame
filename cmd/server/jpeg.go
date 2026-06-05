package main

import "encoding/binary"

// extractJPEGApp1 returns the raw APP1 (EXIF) marker segment from a JPEG,
// or nil if the file has no EXIF APP1.
func extractJPEGApp1(data []byte) []byte {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil
	}
	i := 2
	for i+3 < len(data) {
		if data[i] != 0xFF {
			return nil
		}
		marker := data[i+1]
		if marker == 0xD9 || marker == 0xDA {
			return nil
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		end := i + 2 + segLen
		if end > len(data) {
			return nil
		}
		if marker == 0xE1 && segLen >= 8 && string(data[i+4:i+10]) == "Exif\x00\x00" {
			return data[i:end]
		}
		i = end
	}
	return nil
}

// resetExifOrientation returns a copy of an APP1 segment with two changes:
//  1. The IFD0 orientation tag is set to 1 (TopLeft / no rotation), because
//     imaging.AutoOrientation has already baked the rotation into the pixels.
//  2. The IFD1 next-pointer is zeroed, dropping the stale embedded JPEG
//     thumbnail. Without this, file managers extract the pre-crop, pre-rotate
//     thumbnail and display it in the wrong orientation.
func resetExifOrientation(app1 []byte) []byte {
	if len(app1) < 18 {
		return app1
	}
	// TIFF data begins at byte 10: FF E1 (2) + length (2) + "Exif\0\0" (6)
	tiff := app1[10:]
	if len(tiff) < 8 {
		return app1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return app1
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return app1
	}
	ifd0 := int(bo.Uint32(tiff[4:8]))
	if ifd0+2 > len(tiff) {
		return app1
	}
	n := int(bo.Uint16(tiff[ifd0:]))

	out := make([]byte, len(app1))
	copy(out, app1)
	tiffOut := out[10:]

	// 1. Reset orientation tag to TopLeft.
	for i := range n {
		off := ifd0 + 2 + i*12
		if off+12 > len(tiff) {
			break
		}
		if bo.Uint16(tiff[off:]) == 0x0112 {
			bo.PutUint16(tiffOut[off+8:], 1)
			break
		}
	}

	// 2. Zero the IFD1 next-pointer (4 bytes immediately after IFD0 entries).
	nextPtr := ifd0 + 2 + n*12
	if nextPtr+4 <= len(tiff) {
		bo.PutUint32(tiffOut[nextPtr:], 0)
	}

	return out
}

// injectJPEGApp1 inserts an APP1 segment immediately after the SOI marker of
// a JPEG byte slice and returns the result.
func injectJPEGApp1(dst, app1 []byte) []byte {
	if len(dst) < 2 {
		return dst
	}
	out := make([]byte, 0, len(dst)+len(app1))
	out = append(out, dst[:2]...) // SOI
	out = append(out, app1...)    // EXIF APP1
	out = append(out, dst[2:]...) // remainder
	return out
}
