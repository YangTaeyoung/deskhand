package capture

import (
	"image"
	"testing"
)

// bgraImage returns w×h BGRA pixels in rows of pitch bytes from px, which
// gives the B, G and R of each pixel; the padding of each row is 0xEE.
func bgraImage(w, h, pitch int, px func(x, y int) (b, g, r byte)) []byte {
	buf := make([]byte, pitch*h)
	for i := range buf {
		buf[i] = 0xEE
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			b, g, r := px(x, y)
			i := y*pitch + x*4
			buf[i], buf[i+1], buf[i+2], buf[i+3] = b, g, r, 0
		}
	}
	return buf
}

func rgbaAt(img *image.RGBA, x, y int) [4]byte {
	i := img.PixOffset(x, y)
	return [4]byte(img.Pix[i : i+4])
}

func TestScaleSizeKeepsTheAspectRatio(t *testing.T) {
	for _, c := range []struct{ w, h, max, ow, oh int }{
		{3840, 2160, 1568, 1568, 882},
		{3840, 2160, 0, 3840, 2160},
		{3840, 2160, 3840, 3840, 2160},
		{3840, 2160, 5000, 3840, 2160},
		{1920, 1080, 1568, 1568, 882},
		{3000, 10, 1, 1, 1},
		{1080, 1920, 1568, 1080, 1920},
	} {
		if ow, oh := scaleSize(c.w, c.h, c.max); ow != c.ow || oh != c.oh {
			t.Errorf("scaleSize(%d, %d, %d) = %dx%d, want %dx%d", c.w, c.h, c.max, ow, oh, c.ow, c.oh)
		}
	}
}

func TestScaleBGRAAtFullSizeSwapsToOpaqueRGBA(t *testing.T) {
	const w, h, pitch = 5, 3, 32
	src := bgraImage(w, h, pitch, func(x, y int) (byte, byte, byte) { return byte(x), byte(y), byte(10*x + y) })
	img := scaleBGRA(src, pitch, w, h, w, h)
	if img.Bounds() != image.Rect(0, 0, w, h) {
		t.Fatalf("bounds %v", img.Bounds())
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if got, want := rgbaAt(img, x, y), [4]byte{byte(10*x + y), byte(y), byte(x), 255}; got != want {
				t.Fatalf("pixel (%d,%d) %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestScaleBGRAAveragesTheAreaOfEachPixel(t *testing.T) {
	const w, h = 4, 2
	src := bgraImage(w, h, w*4, func(x, y int) (byte, byte, byte) {
		v := []byte{0, 100, 200, 40}[x]
		if y == 1 {
			v += 20
		}
		return v, v, v
	})
	img := scaleBGRA(src, w*4, w, h, 2, 1)
	for x, want := range []byte{(0 + 100 + 20 + 120) / 4, (200 + 40 + 220 + 60) / 4} {
		if got := rgbaAt(img, x, 0); got != [4]byte{want, want, want, 255} {
			t.Fatalf("pixel %d is %v, want %d", x, got, want)
		}
	}
}

func TestScaleBGRAWithAFractionalRatio(t *testing.T) {
	const w, h, ow, oh = 3840, 2160, 1568, 882
	checker := bgraImage(w, h, w*4, func(x, y int) (byte, byte, byte) {
		if (x+y)%2 == 0 {
			return 255, 255, 255
		}
		return 0, 0, 0
	})
	img := scaleBGRA(checker, w*4, w, h, ow, oh)
	for _, p := range [][2]int{{0, 0}, {1, 1}, {ow / 2, oh / 2}, {ow - 1, oh - 1}, {777, 3}} {
		got := rgbaAt(img, p[0], p[1])
		for c := 0; c < 3; c++ {
			if got[c] < 100 || got[c] > 155 {
				t.Fatalf("checkerboard pixel %v is %v, want about 127", p, got)
			}
		}
	}

	ramp := bgraImage(w, h, w*4, func(x, y int) (byte, byte, byte) { return byte(x * 255 / (w - 1)), 30, 200 })
	img = scaleBGRA(ramp, w*4, w, h, ow, oh)
	last := -1
	for x := 0; x < ow; x++ {
		px := rgbaAt(img, x, oh/3)
		if px[0] != 200 || px[1] != 30 || px[3] != 255 || int(px[2]) < last {
			t.Fatalf("ramp pixel %d is %v after blue %d", x, px, last)
		}
		last = int(px[2])
	}
	if first, end := rgbaAt(img, 0, 0)[2], rgbaAt(img, ow-1, 0)[2]; first > 2 || end < 253 {
		t.Fatalf("ramp ends %d and %d", first, end)
	}
}

func BenchmarkScaleBGRA4KTo1568(b *testing.B) {
	const w, h = 3840, 2160
	src := bgraImage(w, h, w*4, func(x, y int) (byte, byte, byte) { return byte(x), byte(y), byte(x ^ y) })
	ow, oh := scaleSize(w, h, DefaultStillMaxWidth)
	for b.Loop() {
		scaleBGRA(src, w*4, w, h, ow, oh)
	}
}
