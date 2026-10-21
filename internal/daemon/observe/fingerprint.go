package observe

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"math"
)

const (
	// fpCols and fpRows are the cells of a fingerprint: the display split
	// into a grid of that many columns and rows.
	fpCols = 64
	fpRows = 36
	// fpThreshold is how far, in luminance levels of 0–255, the average of
	// one cell has to move for the screen to count as changed.
	fpThreshold = 4.0
	// thumbWidth is the max_width of the still images of a settle.
	thumbWidth = 256
)

// fingerprint is the average luminance of each cell of the display, from
// one still image of it.
type fingerprint struct {
	cells [fpCols * fpRows]float64
}

// differs reports whether some cell of a and b differs by more than
// fpThreshold.
func (a *fingerprint) differs(b *fingerprint) bool {
	for i := range a.cells {
		if math.Abs(a.cells[i]-b.cells[i]) > fpThreshold {
			return true
		}
	}
	return false
}

// span is the part weight of one image row or column that falls in cell.
type span struct {
	cell   int
	weight float64
}

// spans returns, for each of n image pixels along an axis, the cells of
// cells equal parts of that axis it overlaps and by how much.
func spans(n, cells int) [][]span {
	out := make([][]span, n)
	for i := range n {
		a := float64(i) * float64(cells) / float64(n)
		b := float64(i+1) * float64(cells) / float64(n)
		for c := int(a); c < cells && float64(c) < b; c++ {
			w := math.Min(b, float64(c+1)) - math.Max(a, float64(c))
			if w > 0 {
				out[i] = append(out[i], span{cell: c, weight: w})
			}
		}
	}
	return out
}

// fingerprintOf returns the fingerprint of img: each pixel's luminance
// counts toward the cells it overlaps by the overlapped area.
func fingerprintOf(img image.Image) *fingerprint {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	fp := &fingerprint{}
	if w == 0 || h == 0 {
		return fp
	}
	cols, rows := spans(w, fpCols), spans(h, fpRows)
	var sum, wsum [fpCols * fpRows]float64
	row := make([]float64, fpCols)
	rowW := make([]float64, fpCols)
	for y := range h {
		clear(row)
		clear(rowW)
		for x := range w {
			l := luminance(img, b.Min.X+x, b.Min.Y+y)
			for _, c := range cols[x] {
				row[c.cell] += l * c.weight
				rowW[c.cell] += c.weight
			}
		}
		for _, r := range rows[y] {
			base := r.cell * fpCols
			for c := range fpCols {
				sum[base+c] += row[c] * r.weight
				wsum[base+c] += rowW[c] * r.weight
			}
		}
	}
	for i := range sum {
		if wsum[i] > 0 {
			fp.cells[i] = sum[i] / wsum[i]
		}
	}
	return fp
}

// luminance returns the luminance (0–255) of the pixel x, y of img.
func luminance(img image.Image, x, y int) float64 {
	switch m := img.(type) {
	case *image.RGBA:
		o := m.PixOffset(x, y)
		return 0.299*float64(m.Pix[o]) + 0.587*float64(m.Pix[o+1]) + 0.114*float64(m.Pix[o+2])
	case *image.NRGBA:
		o := m.PixOffset(x, y)
		return 0.299*float64(m.Pix[o]) + 0.587*float64(m.Pix[o+1]) + 0.114*float64(m.Pix[o+2])
	case *image.YCbCr:
		return float64(m.Y[m.YOffset(x, y)])
	case *image.Gray:
		return float64(m.Pix[m.PixOffset(x, y)])
	}
	r, g, bl, _ := img.At(x, y).RGBA()
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257
}

// decodeStill decodes a PNG or JPEG still image.
func decodeStill(data []byte, format string) (image.Image, error) {
	if format == "jpeg" {
		return jpeg.Decode(bytes.NewReader(data))
	}
	return png.Decode(bytes.NewReader(data))
}
