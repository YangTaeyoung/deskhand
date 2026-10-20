package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
)

// keyCases are the cases of the keyframe scenario in order.
var keyCases = []string{"start", "keyframe", "recreate", "resize"}

// keyReport compares each key frame of the keyframe scenario with the still
// image taken right after it, in RGB at the still's size: over the fixture
// window (crop, in still pixels) and over the whole image.
type keyReport struct {
	Crop    [4]int              `json:"crop"`
	MaxMAD  float64             `json:"max_mad"`
	MinLit  float64             `json:"min_lit"`
	Cases   map[string]*keyCase `json:"cases"`
	Order   []string            `json:"order"`
	Samples []keyResult         `json:"samples"`
}

// keyCase sums up the samples of one case.
type keyCase struct {
	Samples int     `json:"samples"`
	Passed  int     `json:"passed"`
	Errors  int     `json:"errors"`
	MADCrop summary `json:"mad_crop"`
	MADFull summary `json:"mad_full"`
	Lit     summary `json:"lit_share"`
	ToRecv  summary `json:"sent_to_recv_ms"`
}

// keyResult is one sample: the mean absolute difference (0–255) of the key
// frame from the still over the crop and the whole image, the key frame's
// mean value and share of values above 24 (−1 when not compared), and
// whether it passed.
type keyResult struct {
	Case    string  `json:"case"`
	N       int     `json:"n"`
	Method  string  `json:"still_method"`
	MADCrop float64 `json:"mad_crop"`
	MADFull float64 `json:"mad_full"`
	Mean    float64 `json:"mean"`
	Lit     float64 `json:"lit_share"`
	Passed  bool    `json:"passed"`
	Error   string  `json:"error,omitempty"`
}

// keyPasses reports whether a key frame with lit share lit and crop
// difference mad is the screen the still shows: not black, and within
// maxMAD of it.
func keyPasses(lit, mad, minLit, maxMAD float64) bool {
	return lit >= minLit && !math.IsNaN(mad) && mad <= maxMAD
}

// stillCrop returns the fixture window of rec in the pixels of a still w
// pixels wide of its display, clamped to w×h.
func stillCrop(rec *record, w, h int) [4]int {
	fx, disp := rec.Fixture, rec.Display
	if fx == nil || disp == nil || disp.Width == 0 {
		return [4]int{0, 0, w, h}
	}
	scale := float64(w) / float64(disp.Width)
	x0 := clamp(int(math.Floor(float64(fx.X-disp.X)*scale)), 0, w-1)
	y0 := clamp(int(math.Floor(float64(fx.Y-disp.Y)*scale)), 0, h-1)
	x1 := clamp(int(math.Ceil(float64(fx.X+fx.W-disp.X)*scale)), x0+1, w)
	y1 := clamp(int(math.Ceil(float64(fx.Y+fx.H-disp.Y)*scale)), y0+1, h)
	return [4]int{x0, y0, x1 - x0, y1 - y0}
}

// cropRGB returns the rows of crop c of an RGB image w pixels wide.
func cropRGB(img []byte, w int, c [4]int) []byte {
	out := make([]byte, 0, c[2]*c[3]*3)
	for y := c[1]; y < c[1]+c[3]; y++ {
		lo := (y*w + c[0]) * 3
		out = append(out, img[lo:lo+c[2]*3]...)
	}
	return out
}

// decodeRGB decodes the first frame of path with ffmpeg into w×h RGB.
func decodeRGB(path string, w, h int) ([]byte, error) {
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-i", path, "-frames:v", "1", "-vf",
		fmt.Sprintf("scale=%d:%d:flags=area,format=rgb24", w, h), "-f", "rawvideo", "pipe:1")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg %s: %v: %s", filepath.Base(path), err, stderr.String())
	}
	if out.Len() != w*h*3 {
		return nil, fmt.Errorf("ffmpeg %s gave %d bytes for %dx%d RGB", filepath.Base(path), out.Len(), w, h)
	}
	return out.Bytes(), nil
}

// keySummary compares every key frame of rec with its still.
func keySummary(rec *record, o reportOptions) (*keyReport, error) {
	rep := &keyReport{MaxMAD: o.keyMAD, MinLit: o.keyLit, Cases: map[string]*keyCase{}, Order: keyCases}
	acc := map[string]*struct{ crop, full, lit, recv []float64 }{}
	for _, c := range keyCases {
		rep.Cases[c] = &keyCase{}
		acc[c] = &struct{ crop, full, lit, recv []float64 }{}
	}
	for _, s := range rec.Keys {
		kc, a := rep.Cases[s.Case], acc[s.Case]
		if kc == nil {
			continue
		}
		kc.Samples++
		r := keyResult{Case: s.Case, N: s.N, Method: s.StillMethod, MADCrop: -1, MADFull: -1, Mean: -1, Error: s.Error}
		if r.Error == "" && (s.AUFile == "" || s.Still == "" || s.StillW <= 0 || s.StillH <= 0) {
			r.Error = "no key frame or still"
		}
		if r.Error == "" {
			key, err := decodeRGB(filepath.Join(o.dir, s.AUFile), s.StillW, s.StillH)
			if err != nil {
				return nil, err
			}
			still, err := decodeRGB(filepath.Join(o.dir, s.Still), s.StillW, s.StillH)
			if err != nil {
				return nil, err
			}
			crop := stillCrop(rec, s.StillW, s.StillH)
			rep.Crop = crop
			r.MADFull = meanAbsDiff(key, still)
			r.MADCrop = meanAbsDiff(cropRGB(key, s.StillW, crop), cropRGB(still, s.StillW, crop))
			r.Mean, r.Lit = brightness(key, 24)
			r.Passed = keyPasses(r.Lit, r.MADCrop, o.keyLit, o.keyMAD)
			a.crop, a.full, a.lit = append(a.crop, r.MADCrop), append(a.full, r.MADFull), append(a.lit, r.Lit)
			a.recv = append(a.recv, ms(float64(s.RecvUS-s.SentUS)))
		}
		if r.Error != "" {
			kc.Errors++
		}
		if r.Passed {
			kc.Passed++
		}
		rep.Samples = append(rep.Samples, r)
	}
	for _, c := range keyCases {
		kc, a := rep.Cases[c], acc[c]
		kc.MADCrop, kc.MADFull, kc.Lit, kc.ToRecv = summarize(a.crop), summarize(a.full), summarize(a.lit), summarize(a.recv)
	}
	return rep, nil
}

// printKeys writes rep as Korean Markdown lines.
func printKeys(w io.Writer, rep *keyReport) {
	fmt.Fprintf(w, "- 키프레임: 받은 직후의 screen.capture와 RGB로 비교(fixture 영역 %v, 통과: 밝기 24 넘는 값 비율 ≥ %s, fixture 영역 평균 절대 차이 ≤ %s)\n",
		rep.Crop, f(rep.MinLit, 2), f(rep.MaxMAD, 1))
	for _, c := range rep.Order {
		kc := rep.Cases[c]
		fmt.Fprintf(w, "  - %s: %d/%d 통과(오류 %d), 차이(fixture) %s, 차이(전체) %s, 밝은 값 비율 %s, 요청→수신 %s ms\n",
			c, kc.Passed, kc.Samples, kc.Errors, dist(kc.MADCrop, 2), dist(kc.MADFull, 2), dist(kc.Lit, 3), dist(kc.ToRecv, 1))
	}
	for _, s := range rep.Samples {
		if !s.Passed {
			fmt.Fprintf(w, "  - 실패 %s #%d: 차이 %s, 밝은 값 비율 %s, 평균 %s %s\n", s.Case, s.N, f(s.MADCrop, 2), f(s.Lit, 3), f(s.Mean, 1), s.Error)
		}
	}
}
