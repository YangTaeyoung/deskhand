package capture

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"regexp"
	"testing"
)

func TestEncodeStillRoundTrips(t *testing.T) {
	const w, h = 64, 48
	src := bgraImage(w, h, w*4, func(x, y int) (byte, byte, byte) { return byte(4 * x), byte(5 * y), 200 })
	img := scaleBGRA(src, w*4, w, h, w, h)

	data, err := encodeStill(img, FormatPNG, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(*image.RGBA); !ok || got.Bounds() != img.Bounds() {
		t.Fatalf("PNG decodes to %T %v", got, got.Bounds())
	}
	if !bytes.Equal(got.(*image.RGBA).Pix, img.Pix) {
		t.Fatal("PNG pixels differ")
	}

	data, err = encodeStill(img, FormatJPEG, 90)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != w || cfg.Height != h {
		t.Fatalf("JPEG %+v %v", cfg, err)
	}
	if low, _ := encodeStill(img, FormatJPEG, 10); len(low) >= len(data) {
		t.Fatalf("JPEG quality 10 is %d bytes, quality 90 %d", len(low), len(data))
	}

	if _, err := encodeStill(img, "gif", 0); err == nil {
		t.Fatal("gif encoded")
	}
}

func TestCaptureIDsAreDistinct(t *testing.T) {
	pattern := regexp.MustCompile(`^c-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 100 {
		id := newCaptureID()
		if !pattern.MatchString(id) || seen[id] {
			t.Fatalf("capture id %q", id)
		}
		seen[id] = true
	}
}
