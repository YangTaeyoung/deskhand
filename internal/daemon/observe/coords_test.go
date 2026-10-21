package observe

import (
	"math"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestLocateMapsCapturePixelCentersToDisplayPixels(t *testing.T) {
	right := captureRec{display: 1, width: 1568, height: 882, scale: 0.408, originX: 3840, originY: 0}
	left := captureRec{display: 2, width: 960, height: 540, scale: 0.5, originX: -1920, originY: -1080}
	full := captureRec{display: 0, width: 1920, height: 1080, scale: 1}
	cases := []struct {
		rec                    captureRec
		x, y                   float64
		dx, dy, screenX, scrnY int
	}{
		{right, 400, 300, 981, 736, 4821, 736},
		{right, 400.9, 300.2, 981, 736, 4821, 736},
		{right, 0, 0, 1, 1, 3841, 1},
		{right, 1567, 881, 3841, 2160, 7681, 2160},
		{left, 0, 0, 1, 1, -1919, -1079},
		{left, 959, 539, 1919, 1079, -1, -1},
		{left, 480, 270, 961, 541, -959, -539},
		{full, 1919, 1079, 1919, 1079, 1919, 1079},
		{full, 10, 20, 10, 20, 10, 20},
	}
	for _, c := range cases {
		tg, err := c.rec.locate(c.x, c.y)
		if err != nil {
			t.Fatalf("(%v, %v) on %+v: %v", c.x, c.y, c.rec, err)
		}
		if tg.Display != c.rec.display || tg.X != c.dx || tg.Y != c.dy || tg.ScreenX != c.screenX || tg.ScreenY != c.scrnY {
			t.Fatalf("(%v, %v) on display %d: %+v, want display pixel (%d, %d) screen (%d, %d)", c.x, c.y, c.rec.display, tg, c.dx, c.dy, c.screenX, c.scrnY)
		}
	}

	for _, p := range [][2]float64{{-1, 0}, {0, -0.5}, {1568, 10}, {10, 882}, {math.NaN(), 1}, {1, math.Inf(1)}} {
		_, err := right.locate(p[0], p[1])
		if e := apierr.As(err); err == nil || e.Code != wire.CodeInvalidArgument || e.NextAction != apierr.NextFixParams {
			t.Fatalf("(%v, %v) outside the 1568×882 image: %+v", p[0], p[1], err)
		}
	}
}
