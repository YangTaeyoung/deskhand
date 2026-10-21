package capture

import "time"

// firstImageWait bounds the wait for the first image of a duplication.
const firstImageWait = 500 * time.Millisecond

// frameInfo is DXGI_OUTDUPL_FRAME_INFO.
type frameInfo struct {
	LastPresentTime, LastMouseUpdateTime            int64
	AccumulatedFrames                               uint32
	RectsCoalesced, ProtectedContentMaskedOut       int32
	PointerX, PointerY                              int32
	PointerVisible                                  int32
	TotalMetadataBufferSize, PointerShapeBufferSize uint32
}

// presented reports whether the frame carries a desktop image the
// compositor presented since the frame acquired before.
func (fi *frameInfo) presented() bool {
	return fi.LastPresentTime != 0 && fi.AccumulatedFrames > 0
}

// ddStep is how a Desktop Duplication source gets an image after one
// AcquireNextFrame.
type ddStep int

const (
	// stepNone takes no image.
	stepNone ddStep = iota
	// stepCopy copies the acquired desktop image.
	stepCopy
	// stepGDI takes the image with GDI.
	stepGDI
)

// imageStep decides how the source gets an image after AcquireNextFrame
// gave fi, nil when no frame came within its timeout. waiting is how long
// the source has waited for the first image of its duplication, negative
// once it took one; gdi reports that the source can take an image with
// GDI.
func imageStep(fi *frameInfo, waiting time.Duration, gdi bool) ddStep {
	switch {
	case fi != nil && fi.presented():
		return stepCopy
	case gdi && waiting >= firstImageWait:
		return stepGDI
	}
	return stepNone
}
