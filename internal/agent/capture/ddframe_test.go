package capture

import (
	"testing"
	"time"
)

func TestImageStepTakesOnlyPresentedImagesAndGDIAfterTheWait(t *testing.T) {
	pointerOnly := &frameInfo{LastMouseUpdateTime: 363544826804, PointerVisible: 1, PointerShapeBufferSize: 576}
	presented := &frameInfo{LastPresentTime: 363544901391, AccumulatedFrames: 1, TotalMetadataBufferSize: 16}
	presentNoFrames := &frameInfo{LastPresentTime: 363544901391}
	for _, c := range []struct {
		name    string
		fi      *frameInfo
		waiting time.Duration
		gdi     bool
		want    ddStep
	}{
		{"first frame of a duplication without a present", pointerOnly, 0, true, stepNone},
		{"first frame of a duplication with a present", presented, time.Millisecond, true, stepCopy},
		{"presented frame after the first image", presented, -1, true, stepCopy},
		{"pointer update after the first image", pointerOnly, -1, true, stepNone},
		{"present time without accumulated frames", presentNoFrames, 10 * time.Millisecond, true, stepNone},
		{"no frame before the wait ends", nil, firstImageWait - time.Millisecond, true, stepNone},
		{"no frame once the wait ended", nil, firstImageWait, true, stepGDI},
		{"pointer update once the wait ended", pointerOnly, firstImageWait + time.Millisecond, true, stepGDI},
		{"presented frame once the wait ended", presented, 2 * firstImageWait, true, stepCopy},
		{"no frame once the wait ended without GDI", nil, 2 * firstImageWait, false, stepNone},
		{"no frame after the first image", nil, -1, true, stepNone},
	} {
		if got := imageStep(c.fi, c.waiting, c.gdi); got != c.want {
			t.Errorf("%s: step %d, want %d", c.name, got, c.want)
		}
	}
}
