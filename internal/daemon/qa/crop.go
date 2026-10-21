package qa

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// displayOf returns the index of the display.list display that holds the
// centre of the win.list window w, or def when the displays cannot be read
// or none holds it.
func (r *runner) displayOf(ctx context.Context, w map[string]any, def int) int {
	res, _, err := r.call(ctx, "display.list", proto.Message{})
	if err != nil {
		return def
	}
	cx, cy := number(w["x"])+number(w["w"])/2, number(w["y"])+number(w["h"])/2
	list, _ := res["displays"].([]any)
	for _, item := range list {
		d, ok := item.(map[string]any)
		if !ok {
			continue
		}
		x, y := number(d["x"]), number(d["y"])
		if cx >= x && cx < x+number(d["width"]) && cy >= y && cy < y+number(d["height"]) {
			return int(number(d["index"]))
		}
	}
	return def
}

// cropToWindow cuts the PNG data of the screen.capture answer res down to
// the win.list window w, whose x, y, w and h are virtual desktop pixels,
// through the capture's origin and scale. It returns the cut PNG and res
// with its size; ref names the window and display the display in errors.
func (r *runner) cropToWindow(data []byte, res proto.Message, w map[string]any, ref string, display int) ([]byte, proto.Message, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, apierr.Internal("에이전트가 보낸 화면을 읽지 못했습니다. 에이전트를 업데이트하세요")
	}
	origin, _ := res["origin"].(map[string]any)
	ox, oy := number(origin["x"]), number(origin["y"])
	scale := number(res["scale"])
	if scale <= 0 {
		scale = 1
	}
	wx, wy := number(w["x"])-ox, number(w["y"])-oy
	b := img.Bounds()
	cut := image.Rect(int(math.Floor(wx*scale)), int(math.Floor(wy*scale)),
		int(math.Ceil((wx+number(w["w"]))*scale)), int(math.Ceil((wy+number(w["h"]))*scale))).Add(b.Min).Intersect(b)
	if cut.Empty() {
		return nil, nil, apierr.New(wire.CodeNotFound, fmt.Sprintf("창 %q가 디스플레이 %d 화면에 보이지 않습니다. 창을 복원하거나 화면 안으로 옮긴 뒤 다시 실행하세요", r.sc.Scrub(ref), display), false, "")
	}
	part := image.NewNRGBA(image.Rect(0, 0, cut.Dx(), cut.Dy()))
	draw.Draw(part, part.Bounds(), img, cut.Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, part); err != nil {
		return nil, nil, apierr.Internal(fmt.Sprintf("창 화면을 PNG로 만들지 못했습니다(%v). 다시 실행하세요", err))
	}
	out := proto.Message{}
	for k, v := range res {
		if k != "data" {
			out[k] = v
		}
	}
	out["width"], out["height"] = cut.Dx(), cut.Dy()
	return buf.Bytes(), out, nil
}

// number reads a JSON number, 0 when v is not one.
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
