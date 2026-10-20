package capture

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
)

// stillOrSkip takes a still image and skips the test, naming the reason,
// when the session cannot be captured now.
func stillOrSkip(t *testing.T, index int, format string, maxWidth, quality int) StillResult {
	t.Helper()
	res, err := CaptureStill(index, format, maxWidth, quality)
	var u *Unavailable
	if errors.As(err, &u) {
		t.Skipf("the session cannot be captured now: %v", u)
	}
	if err != nil {
		t.Fatalf("CaptureStill(%d, %s, %d, %d): %v", index, format, maxWidth, quality, err)
	}
	return res
}

// displaysOrSkip lists the displays and skips the test in a session
// without any.
func displaysOrSkip(t *testing.T) []display.Info {
	t.Helper()
	list, err := display.List()
	if err != nil || len(list) == 0 {
		t.Skipf("this session has no displays (%v); run the test through scripts/wintest.sh", err)
	}
	return list
}

// nonBlackShare is the share of every fourth pixel of every fourth row of
// img whose color is brighter than near-black.
func nonBlackShare(img image.Image) float64 {
	b := img.Bounds()
	var n, lit int
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := img.At(x, y).RGBA()
			n++
			if r>>8 > 8 || g>>8 > 8 || bl>>8 > 8 {
				lit++
			}
		}
	}
	return float64(lit) / float64(n)
}

var captureIDPattern = regexp.MustCompile(`^c-[0-9a-f]{12}$`)

func TestCaptureStillPNG(t *testing.T) {
	d := displaysOrSkip(t)[0]
	for _, c := range []struct {
		format            string
		maxWidth, quality int
		wantW, wantH      int
		decode            func([]byte) (image.Image, error)
	}{
		{FormatPNG, 0, 0, d.Width, d.Height, decodePNG},
		{FormatPNG, d.Width + 100, 0, d.Width, d.Height, decodePNG},
		{FormatPNG, DefaultStillMaxWidth, 0, DefaultStillMaxWidth, int(math.Round(float64(d.Height) * DefaultStillMaxWidth / float64(d.Width))), decodePNG},
		{FormatJPEG, DefaultStillMaxWidth, DefaultJPEGQuality, DefaultStillMaxWidth, int(math.Round(float64(d.Height) * DefaultStillMaxWidth / float64(d.Width))), decodeJPEG},
	} {
		res := stillOrSkip(t, 0, c.format, c.maxWidth, c.quality)
		t.Logf("%s max_width %d: %dx%d scale %.4f method %s, %d bytes, origin (%d,%d) dpi %d", c.format, c.maxWidth,
			res.Width, res.Height, res.Scale, res.Method, len(res.Data), res.OriginX, res.OriginY, res.DPI)
		img, err := c.decode(res.Data)
		if err != nil {
			t.Fatalf("%s max_width %d: decode: %v", c.format, c.maxWidth, err)
		}
		if b := img.Bounds(); b.Dx() != c.wantW || b.Dy() != c.wantH || res.Width != c.wantW || res.Height != c.wantH {
			t.Fatalf("%s max_width %d: image %v, result %dx%d, want %dx%d", c.format, c.maxWidth, b, res.Width, res.Height, c.wantW, c.wantH)
		}
		if share := nonBlackShare(img); share < 0.01 {
			t.Fatalf("%s max_width %d: %.4f of the pixels are not black", c.format, c.maxWidth, share)
		}
		if want := float64(c.wantW) / float64(d.Width); math.Abs(res.Scale-want) > 1e-9 {
			t.Fatalf("scale %v, want %v", res.Scale, want)
		}
		if res.OriginX != d.X || res.OriginY != d.Y || res.DPI != d.DPI || !captureIDPattern.MatchString(res.CaptureID) ||
			res.MonoUS == 0 || !slices.Contains([]string{MethodDD, MethodGDI}, res.Method) {
			t.Fatalf("result %+v for display %+v", res, d)
		}
	}
}

func TestCaptureStillOfEachDisplayHasItsOrigin(t *testing.T) {
	ids := map[string]bool{}
	for _, d := range displaysOrSkip(t) {
		res := stillOrSkip(t, d.Index, FormatPNG, 320, 0)
		if res.OriginX != d.X || res.OriginY != d.Y || res.Width != 320 || res.DPI != d.DPI || ids[res.CaptureID] {
			t.Fatalf("display %+v: result %dx%d origin (%d,%d) dpi %d id %s", d, res.Width, res.Height, res.OriginX, res.OriginY, res.DPI, res.CaptureID)
		}
		ids[res.CaptureID] = true
	}
}

func TestCaptureStillOfAMissingDisplayIsNoDisplay(t *testing.T) {
	list := displaysOrSkip(t)
	if _, err := CaptureStill(len(list), FormatPNG, 0, 0); !errors.Is(err, ErrNoDisplay) {
		t.Fatalf("CaptureStill of display %d: %v", len(list), err)
	}
}

func TestCaptureStillLatency(t *testing.T) {
	d := displaysOrSkip(t)[0]
	for _, maxWidth := range []int{0, DefaultStillMaxWidth} {
		if err := onStillWorker(func(c *devCache) error { c.closeAll(); return nil }); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		stillOrSkip(t, 0, FormatPNG, maxWidth, 0)
		cold := time.Since(start)
		var took []time.Duration
		method := ""
		for range 10 {
			start := time.Now()
			res := stillOrSkip(t, 0, FormatPNG, maxWidth, 0)
			took = append(took, time.Since(start))
			method = res.Method
		}
		slices.Sort(took)
		p50 := took[len(took)/2]
		t.Logf("display %dx%d max_width %d (%s): new device %v; kept device p50 %v, min %v, max %v", d.Width, d.Height, maxWidth, method, cold, p50, took[0], took[len(took)-1])
		if p50 > 2*time.Second {
			t.Fatalf("p50 %v of a still at max_width %d", p50, maxWidth)
		}
	}
}

func decodePNG(b []byte) (image.Image, error)  { return png.Decode(bytes.NewReader(b)) }
func decodeJPEG(b []byte) (image.Image, error) { return jpeg.Decode(bytes.NewReader(b)) }
