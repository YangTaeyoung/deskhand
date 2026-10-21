package observe

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// captureTimeout bounds one screen.capture.
const captureTimeout = 30 * time.Second

// CaptureParams are the parameters of a capture: Display (0 the primary),
// MaxWidth (nil for 1568, 0 for the display's width), Format (png when "",
// or jpeg) and Quality (jpeg only, nil for the agent's default).
type CaptureParams struct {
	Display  int
	MaxWidth *int
	Format   string
	Quality  *int
}

// Point is a point on the virtual desktop.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Capture is a still image of a display: its capture_id, the image size,
// its scale to the display, the display's origin on the virtual desktop,
// when the daemon got it and the image file.
type Capture struct {
	CaptureID            string         `json:"capture_id"`
	HostID               string         `json:"host_id"`
	Display              int            `json:"display"`
	Width                int            `json:"width"`
	Height               int            `json:"height"`
	Scale                float64        `json:"scale"`
	Origin               Point          `json:"origin"`
	DPI                  int            `json:"dpi"`
	TS                   string         `json:"ts"`
	MonoUS               uint64         `json:"mono_us"`
	Method               string         `json:"method"`
	Format               string         `json:"format"`
	ConnectionGeneration uint64         `json:"connection_generation"`
	ExpiresAt            string         `json:"expires_at"`
	Artifact             model.Artifact `json:"artifact"`

	rec *captureRec
}

// Capture takes a still image of a display of hostID through the agent's
// screen.capture, stores it as a screenshot artifact and keeps its
// coordinates under its capture_id for CaptureTTL.
func (s *Service) Capture(ctx context.Context, hostID string, p CaptureParams) (Capture, error) {
	a := s.agent(hostID)
	if a == nil {
		return Capture{}, apierr.NotFound(fmt.Sprintf("PC %s가 등록돼 있지 않습니다. host.list로 PC를 확인하세요", hostID))
	}
	format := p.Format
	if format == "" {
		format = "png"
	}
	if format != "png" && format != "jpeg" {
		return Capture{}, apierr.Invalid(fmt.Sprintf("format %q를 모릅니다. png나 jpeg로 주세요", p.Format))
	}
	if p.Display < 0 {
		return Capture{}, apierr.Invalid(fmt.Sprintf("display %d는 쓸 수 없습니다. 0 이상의 디스플레이 번호를 주세요", p.Display))
	}
	params := proto.Message{"display": p.Display, "format": format}
	if p.MaxWidth != nil {
		if *p.MaxWidth < 0 {
			return Capture{}, apierr.Invalid("max_width는 0 이상이어야 합니다. 0은 디스플레이 폭 그대로입니다")
		}
		params["max_width"] = *p.MaxWidth
	}
	if p.Quality != nil {
		params["quality"] = *p.Quality
	}
	epoch := s.epoch(hostID)
	cctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()
	info := conn.CallInfo{}
	res, err := a.Call(cctx, "screen.capture", params, conn.CallOpts{Info: &info})
	if err != nil {
		return Capture{}, captureError(err, p.Display)
	}
	got := time.Now()
	data, err := base64.StdEncoding.DecodeString(res.String("data"))
	w, h, scale := res.Int("width"), res.Int("height"), floatOf(res["scale"])
	if err != nil || len(data) == 0 || w <= 0 || h <= 0 || !(scale > 0) || math.IsInf(scale, 0) {
		return Capture{}, apierr.Internal("PC의 에이전트가 보낸 화면 이미지를 읽지 못했습니다. 다시 캡처하고, 계속되면 에이전트를 업데이트하세요")
	}
	id := res.String("capture_id")
	if id == "" {
		id = newCaptureID()
	}
	var fp *fingerprint
	if img, err := decodeStill(data, format); err == nil {
		fp = fingerprintOf(img)
	} else {
		s.lg.Printf("observe: decode capture %s of %s: %v", id, hostID, err)
	}
	art, err := s.saveImage(hostID, id, format, data, got)
	if err != nil {
		return Capture{}, apierr.Internal(fmt.Sprintf("캡처한 화면을 저장하지 못했습니다(%v). 디스크 공간과 %s의 권한을 확인하세요", err, s.opt.Dir))
	}
	origin, _ := res["origin"].(map[string]any)
	o := proto.Message(origin)
	rec := &captureRec{id: id, hostID: hostID, display: p.Display, width: w, height: h, scale: scale, originX: o.Int("x"), originY: o.Int("y"),
		connGen: info.Generation, epoch: epoch, fp: fp, at: got, artifact: art}
	s.remember(rec)
	s.setLastFP(hostID, fp)
	var mono uint64
	if v := floatOf(res["mono_us"]); v > 0 {
		mono = uint64(v)
	}
	return Capture{CaptureID: id, HostID: hostID, Display: p.Display, Width: w, Height: h, Scale: scale, Origin: Point{X: rec.originX, Y: rec.originY},
		DPI: res.Int("dpi"), TS: model.Time(got), MonoUS: mono, Method: res.String("method"), Format: format, ConnectionGeneration: info.Generation,
		ExpiresAt: model.Time(got.Add(s.opt.CaptureTTL)), Artifact: art, rec: rec}, nil
}

// thumb takes a still image of display at thumbWidth that is neither
// stored nor given a capture_id, and returns its fingerprint.
func (s *Service) thumb(ctx context.Context, a Agent, display int) (*fingerprint, error) {
	cctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()
	res, err := a.Call(cctx, "screen.capture", proto.Message{"display": display, "format": "png", "max_width": thumbWidth}, conn.CallOpts{})
	if err != nil {
		return nil, captureError(err, display)
	}
	data, err := base64.StdEncoding.DecodeString(res.String("data"))
	if err != nil {
		return nil, apierr.Internal("PC의 에이전트가 보낸 화면 이미지를 읽지 못했습니다. 에이전트를 업데이트하세요")
	}
	img, err := decodeStill(data, "png")
	if err != nil {
		return nil, apierr.Internal(fmt.Sprintf("PC의 에이전트가 보낸 화면 이미지를 읽지 못했습니다(%v). 에이전트를 업데이트하세요", err))
	}
	return fingerprintOf(img), nil
}

func floatOf(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case uint64:
		return float64(n)
	}
	return 0
}

// newCaptureID returns "c-" and 12 random hex digits.
func newCaptureID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "c-" + hex.EncodeToString(b)
}

// captureReason is the fixed reason the agent puts in parentheses in the
// error of a capture it cannot take (docs/protocol.md §3.1).
var captureReason = regexp.MustCompile(`\((session0|rdp_output_suppressed|secure_desktop|session_locked|session_disconnected|access_lost|no_output)\)`)

// captureError gives the agent's refusal of a capture of display a
// sentence that names the next step, keeping its code and retryable.
func captureError(err error, display int) error {
	e := apierr.As(err)
	reason := ""
	if m := captureReason.FindStringSubmatch(e.Message); m != nil {
		reason = m[1]
	}
	switch {
	case e.Code == wire.CodeSessionLocked || reason == "session_locked":
		return apierr.New(e.Code, "PC가 잠겨 있어 화면을 찍을 수 없습니다. PC에서 잠금을 푼 뒤 다시 캡처하세요", false, apierr.NextCheckState)
	case reason == "secure_desktop":
		return apierr.New(e.Code, "PC에 보안 화면(UAC·로그온 화면)이 떠 있어 찍을 수 없습니다. PC에서 그 화면에 답한 뒤 다시 캡처하세요", false, apierr.NextCheckState)
	case reason == "session_disconnected" || reason == "rdp_output_suppressed":
		return apierr.New(e.Code, fmt.Sprintf("PC의 사용자 세션이 화면을 그리지 않습니다(%s). PC에 콘솔로 로그인하거나 원격 데스크톱 창을 연 뒤 다시 캡처하세요", reason), false, apierr.NextCheckState)
	case reason == "session0":
		return apierr.New(e.Code, "PC의 에이전트가 사용자 세션 밖(Session 0)에서 돌고 있어 화면을 찍을 수 없습니다. deskhand host doctor로 확인하세요", false, apierr.NextHostDoctor)
	case reason == "access_lost":
		return apierr.New(e.Code, "PC의 화면 캡처가 잠시 끊겼습니다. 잠시 뒤 다시 캡처하세요", true, apierr.NextRetry)
	case reason == "no_output" || e.Code == wire.CodeNotFound:
		return apierr.New(wire.CodeNotFound, fmt.Sprintf("PC에 디스플레이 %d가 없습니다. session.inspect로 디스플레이를 확인한 뒤 다시 캡처하세요", display), false, apierr.NextFixParams)
	case e.Code == wire.CodeCapabilityUnsupported:
		return apierr.New(e.Code, "이 PC의 에이전트는 화면을 찍을 수 없습니다. Windows PC의 에이전트에서 캡처하세요", false, "")
	case e.Code == wire.CodeInvalidArgument && e.NextAction == "":
		return apierr.New(e.Code, fmt.Sprintf("PC의 에이전트가 캡처 요청을 받아들이지 않았습니다(%s). 이미지가 크면 max_width를 줄이거나 format을 jpeg로 다시 캡처하세요", e.Message), false, apierr.NextFixParams)
	}
	return err
}

// saveImage writes the image data of capture id of hostID and returns its
// artifact; it prunes the images older than FileKeep at most once every
// pruneEvery.
func (s *Service) saveImage(hostID, id, format string, data []byte, at time.Time) (model.Artifact, error) {
	dir := filepath.Join(s.opt.Dir, hostID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return model.Artifact{}, err
	}
	ext, mime := ".png", "image/png"
	if format == "jpeg" {
		ext, mime = ".jpg", "image/jpeg"
	}
	name := id + ext
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return model.Artifact{}, err
	}
	sum := sha256.Sum256(data)
	s.prune(at)
	return model.Artifact{ArtifactID: ids.New(ids.Artifact), Kind: "screenshot", Name: name, Path: p, Size: int64(len(data)), MIME: mime,
		SHA256: hex.EncodeToString(sum[:]), CreatedAt: model.Time(at)}, nil
}

// prune removes the capture images older than FileKeep, once every
// pruneEvery.
func (s *Service) prune(now time.Time) {
	s.mu.Lock()
	if now.Sub(s.prunedAt) < pruneEvery {
		s.mu.Unlock()
		return
	}
	s.prunedAt = now
	s.mu.Unlock()
	hosts, err := os.ReadDir(s.opt.Dir)
	if err != nil {
		return
	}
	for _, h := range hosts {
		if !h.IsDir() {
			continue
		}
		dir := filepath.Join(s.opt.Dir, h.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			fi, err := f.Info()
			if err == nil && fi.Mode().IsRegular() && now.Sub(fi.ModTime()) > s.opt.FileKeep {
				_ = os.Remove(filepath.Join(dir, f.Name()))
			}
		}
	}
}

// remember keeps rec in the coordinate table and drops the records older
// than CaptureTTL.
func (s *Service) remember(rec *captureRec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.captures {
		if rec.at.Sub(r.at) > s.opt.CaptureTTL {
			delete(s.captures, id)
		}
	}
	s.captures[rec.id] = rec
}

func staleCapture(format string, a ...any) *apierr.Error {
	return apierr.New(wire.CodeStaleTarget, fmt.Sprintf(format, a...), false, apierr.NextScreenCapture)
}

// lookup returns the record of captureID for an input on hostID, or why
// its coordinates cannot be used: STALE_TARGET when it is unknown, older
// than CaptureTTL, or from before a display change of the host, and
// INVALID_ARGUMENT when it is another host's.
func (s *Service) lookup(hostID, captureID string) (*captureRec, error) {
	if captureID == "" {
		return nil, apierr.Invalid("capture_id가 없습니다. screen.capture로 화면을 찍고 그 capture_id와 이미지 좌표를 주세요")
	}
	s.mu.Lock()
	rec := s.captures[captureID]
	epoch := s.hostLocked(hostID).epoch
	s.mu.Unlock()
	switch {
	case rec == nil:
		return nil, staleCapture("capture_id %s를 모릅니다(%s이 지났거나 데몬이 다시 시작됐습니다). screen.capture로 다시 캡처한 뒤 새 capture_id의 좌표로 다시 시도하세요", captureID, s.opt.CaptureTTL)
	case rec.hostID != hostID:
		return nil, apierr.Invalid(fmt.Sprintf("capture_id %s는 다른 PC(%s)의 화면입니다. 이 PC를 screen.capture로 찍은 capture_id를 주세요", captureID, rec.hostID))
	case time.Since(rec.at) > s.opt.CaptureTTL:
		return nil, staleCapture("capture_id %s는 %s이 지나 쓸 수 없습니다. screen.capture로 다시 캡처한 뒤 새 capture_id의 좌표로 다시 시도하세요", captureID, s.opt.CaptureTTL)
	case rec.epoch != epoch:
		return nil, staleCapture("capture_id %s를 찍은 뒤 PC의 디스플레이 구성이 바뀌었습니다. screen.capture로 다시 캡처한 뒤 새 capture_id의 좌표로 다시 시도하세요", captureID)
	}
	return rec, nil
}

// valid returns nil while rec's coordinates hold for an input on the
// connection connGen of its host.
func (s *Service) valid(rec *captureRec, connGen uint64) error {
	if connGen != rec.connGen {
		return staleCapture("capture_id %s는 PC와 다시 연결되기 전(연결 세대 %d, 지금 %d)의 화면입니다. screen.capture로 다시 캡처한 뒤 새 capture_id의 좌표로 다시 시도하세요",
			rec.id, rec.connGen, connGen)
	}
	if s.epoch(rec.hostID) != rec.epoch {
		return staleCapture("capture_id %s를 찍은 뒤 PC의 디스플레이 구성이 바뀌었습니다. screen.capture로 다시 캡처한 뒤 새 capture_id의 좌표로 다시 시도하세요", rec.id)
	}
	return nil
}

// Locate maps the image point (x, y) of capture captureID to the display
// pixel it lands on, for an input on hostID: STALE_TARGET when the
// capture's coordinates no longer hold (lookup, and another connection
// generation than the host's now), INVALID_ARGUMENT for a point outside
// the image.
func (s *Service) Locate(hostID, captureID string, x, y float64) (Target, error) {
	rec, err := s.lookup(hostID, captureID)
	if err != nil {
		return Target{}, err
	}
	if a := s.agent(hostID); a != nil {
		if err := s.valid(rec, a.Generation()); err != nil {
			return Target{}, err
		}
	}
	return rec.locate(x, y)
}
