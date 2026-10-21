package observe

import (
	"fmt"
	"math"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

// captureRec is what the coordinate table keeps of one screen.capture:
// the image size, its scale to the display and the display's origin on
// the virtual desktop, the connection generation the image came on and
// the host's display epoch before it was asked for.
type captureRec struct {
	id, hostID    string
	display       int
	width, height int
	scale         float64
	originX       int
	originY       int
	connGen       uint64
	epoch         uint64
	fp            *fingerprint
	at            time.Time
	artifact      model.Artifact
}

// Target is where a capture coordinate lands: Display and the pixel X, Y
// of that display (from its top left), and ScreenX, ScreenY on the
// virtual desktop.
type Target struct {
	CaptureID string `json:"capture_id,omitempty"`
	Display   int    `json:"display"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	ScreenX   int    `json:"screen_x"`
	ScreenY   int    `json:"screen_y"`
}

// displaySize is the display's size in pixels: the image size divided by
// the scale, rounded.
func (r *captureRec) displaySize() (int, int) {
	return int(math.Round(float64(r.width) / r.scale)), int(math.Round(float64(r.height) / r.scale))
}

// locate maps the image point (x, y) to display pixels: the image pixel
// that holds the point is floor(x), floor(y), and its center
// (floor(x)+0.5)/scale lands in display pixel floor of that, kept within
// the display. A point outside the image is INVALID_ARGUMENT.
func (r *captureRec) locate(x, y float64) (Target, error) {
	if !inImage(x, r.width) || !inImage(y, r.height) {
		return Target{}, apierr.Invalid(fmt.Sprintf("(%v, %v)는 capture %s의 %d×%d 이미지 밖입니다. x는 0–%d, y는 0–%d 안의 이미지 좌표로 주세요",
			x, y, r.id, r.width, r.height, r.width-1, r.height-1))
	}
	dw, dh := r.displaySize()
	dx := toDisplay(x, r.scale, dw)
	dy := toDisplay(y, r.scale, dh)
	return Target{CaptureID: r.id, Display: r.display, X: dx, Y: dy, ScreenX: r.originX + dx, ScreenY: r.originY + dy}, nil
}

func inImage(v float64, size int) bool {
	return !math.IsNaN(v) && v >= 0 && v < float64(size)
}

// toDisplay maps image coordinate v to the display pixel under the center
// of its image pixel, within 0 to size-1.
func toDisplay(v, scale float64, size int) int {
	p := int(math.Floor((math.Floor(v) + 0.5) / scale))
	return min(max(p, 0), max(size-1, 0))
}
