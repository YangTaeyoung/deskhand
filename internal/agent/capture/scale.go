package capture

import (
	"image"
	"math"
	"runtime"
	"sync"
)

// scaleSize returns the size of a w×h image fitted to maxWidth: w×h when
// maxWidth is 0 or at least w, else maxWidth wide and as high as keeps the
// aspect ratio, rounded and at least 1.
func scaleSize(w, h, maxWidth int) (int, int) {
	if maxWidth <= 0 || maxWidth >= w {
		return w, h
	}
	oh := int(math.Round(float64(h) * float64(maxWidth) / float64(w)))
	return maxWidth, max(oh, 1)
}

// weightOne is the sum of the weights of one output pixel in taps.
const weightOne = 1 << 14

// taps are the source pixels of each output pixel of a resampling from n
// to m pixels (m ≤ n): output i reads the source pixels from start[i] on
// with the weights w[off[i]:off[i+1]], which sum to weightOne.
type taps struct {
	start []int
	off   []int
	w     []uint32
}

// areaTaps weighs each source pixel by the length it overlaps the output
// pixel, the output pixel i covering the source span [i*n/m, (i+1)*n/m).
func areaTaps(n, m int) taps {
	t := taps{start: make([]int, m), off: make([]int, m+1)}
	for i := 0; i < m; i++ {
		lo, hi := i*n, (i+1)*n
		j0, j1 := lo/m, (hi+m-1)/m
		t.start[i] = j0
		t.off[i] = len(t.w)
		var sum, bigW uint32
		big := len(t.w)
		for j := j0; j < j1; j++ {
			overlap := min(hi, (j+1)*m) - max(lo, j*m)
			wt := uint32(uint64(overlap) * weightOne / uint64(n))
			if wt > bigW {
				big, bigW = len(t.w), wt
			}
			t.w = append(t.w, wt)
			sum += wt
		}
		t.w[big] += weightOne - sum
	}
	t.off[m] = len(t.w)
	return t
}

// scaleBGRA returns the w×h BGRA image src, rows of pitch bytes, as an
// opaque ow×oh RGBA image (ow ≤ w, oh ≤ h). Each output pixel is the
// average of the source area it covers.
func scaleBGRA(src []byte, pitch, w, h, ow, oh int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, ow, oh))
	if ow == w && oh == h {
		eachBand(h, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				s := src[y*pitch : y*pitch+w*4]
				d := img.Pix[y*img.Stride : y*img.Stride+w*4]
				for x := 0; x < len(s); x += 4 {
					d[x], d[x+1], d[x+2], d[x+3] = s[x+2], s[x+1], s[x], 255
				}
			}
		})
		return img
	}
	xt, yt := areaTaps(w, ow), areaTaps(h, oh)
	eachBand(oh, func(y0, y1 int) {
		row := make([]uint32, ow*3)
		acc := make([]uint32, ow*3)
		rowY := -1
		for oy := y0; oy < y1; oy++ {
			clear(acc)
			j := yt.start[oy]
			for k := yt.off[oy]; k < yt.off[oy+1]; k++ {
				if j != rowY {
					resampleRow(src[j*pitch:j*pitch+w*4], xt, row)
					rowY = j
				}
				wy := yt.w[k]
				for i, v := range row {
					acc[i] += v * wy
				}
				j++
			}
			d := img.Pix[oy*img.Stride : oy*img.Stride+ow*4]
			for x := 0; x < ow; x++ {
				a := acc[x*3 : x*3+3]
				d[x*4] = byte((a[2] + 1<<21) >> 22)
				d[x*4+1] = byte((a[1] + 1<<21) >> 22)
				d[x*4+2] = byte((a[0] + 1<<21) >> 22)
				d[x*4+3] = 255
			}
		}
	})
	return img
}

// resampleRow writes the B, G and R of each output pixel of the BGRA row
// to out, in units of 1/256 of a channel step.
func resampleRow(row []byte, t taps, out []uint32) {
	for x, start := range t.start {
		var b, g, r uint32
		s := start * 4
		for _, wt := range t.w[t.off[x]:t.off[x+1]] {
			b += uint32(row[s]) * wt
			g += uint32(row[s+1]) * wt
			r += uint32(row[s+2]) * wt
			s += 4
		}
		out[x*3], out[x*3+1], out[x*3+2] = (b+1<<5)>>6, (g+1<<5)>>6, (r+1<<5)>>6
	}
}

// eachBand calls fn for consecutive bands of the rows [0, n), in parallel
// on up to GOMAXPROCS goroutines, and waits for them.
func eachBand(n int, fn func(y0, y1 int)) {
	workers := min(runtime.GOMAXPROCS(0), max(n/32, 1))
	per := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for y0 := 0; y0 < n; y0 += per {
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			fn(y0, y1)
		}(y0, min(y0+per, n))
	}
	wg.Wait()
}
