package capture

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
)

// pngEncoder writes PNG images at the fastest compression.
var pngEncoder = png.Encoder{CompressionLevel: png.BestSpeed}

// encodeStill encodes img as format, FormatPNG or FormatJPEG; quality
// (1–100) applies to JPEG.
func encodeStill(img *image.RGBA, format string, quality int) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case FormatPNG:
		if err := pngEncoder.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encoding the PNG: %w", err)
		}
	case FormatJPEG:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encoding the JPEG: %w", err)
		}
	default:
		return nil, fmt.Errorf("format %q is neither %s nor %s", format, FormatPNG, FormatJPEG)
	}
	return buf.Bytes(), nil
}

// newCaptureID returns "c-" and 12 random hex digits.
func newCaptureID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "c-" + hex.EncodeToString(b)
}
