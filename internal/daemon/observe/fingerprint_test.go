package observe

import (
	"image"
	"image/color"
	"testing"
)

// scene is a 1920×1080 white desktop with a dark window frame and text
// lines, and the extra rectangles.
func scene(extra ...image.Rectangle) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	fill := func(r image.Rectangle, c color.RGBA) {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	fill(img.Bounds(), color.RGBA{250, 250, 250, 255})
	fill(image.Rect(200, 150, 1200, 750), color.RGBA{40, 40, 60, 255})
	fill(image.Rect(210, 180, 1190, 740), color.RGBA{255, 255, 255, 255})
	for y := 200; y < 700; y += 20 {
		fill(image.Rect(230, y, 900, y+9), color.RGBA{30, 30, 30, 255})
	}
	for _, r := range extra {
		fill(r, color.RGBA{20, 120, 220, 255})
	}
	return img
}

// areaScale shrinks src to w×h by exact area averaging, as the agent's
// still capture does.
func areaScale(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	cols, rows := spans(sw, w), spans(sh, h)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	acc := make([][3]float64, w*h)
	wsum := make([]float64, w*h)
	for y := 0; y < sh; y++ {
		for _, ry := range rows[y] {
			for x := 0; x < sw; x++ {
				o := src.PixOffset(x, y)
				for _, cx := range cols[x] {
					k := ry.cell*w + cx.cell
					wt := ry.weight * cx.weight
					acc[k][0] += float64(src.Pix[o]) * wt
					acc[k][1] += float64(src.Pix[o+1]) * wt
					acc[k][2] += float64(src.Pix[o+2]) * wt
					wsum[k] += wt
				}
			}
		}
	}
	for k := range acc {
		o := k * 4
		dst.Pix[o] = uint8(acc[k][0]/wsum[k] + 0.5)
		dst.Pix[o+1] = uint8(acc[k][1]/wsum[k] + 0.5)
		dst.Pix[o+2] = uint8(acc[k][2]/wsum[k] + 0.5)
		dst.Pix[o+3] = 255
	}
	return dst
}

func TestFingerprintComparesScreensAcrossImageWidths(t *testing.T) {
	base := scene()
	fullA := fingerprintOf(areaScale(base, 1568, 882))
	thumbA := fingerprintOf(areaScale(base, thumbWidth, 144))
	if fullA.differs(thumbA) {
		t.Fatal("the same screen at two widths differs")
	}
	caret := scene(image.Rect(500, 350, 501, 362))
	if thumbA.differs(fingerprintOf(areaScale(caret, thumbWidth, 144))) {
		t.Fatal("a 1×12 caret counts as a change")
	}
	box := scene(image.Rect(500, 350, 508, 358))
	if !thumbA.differs(fingerprintOf(areaScale(box, thumbWidth, 144))) {
		t.Fatal("an 8×8 checkbox change is missed")
	}
	if !fullA.differs(fingerprintOf(areaScale(box, 1568, 882))) {
		t.Fatal("an 8×8 checkbox change is missed at full width")
	}
	button := scene(image.Rect(1500, 900, 1600, 930))
	if !fullA.differs(fingerprintOf(areaScale(button, thumbWidth, 144))) {
		t.Fatal("a new 100×30 button is missed across widths")
	}
}
