package wire

import (
	"encoding/binary"
	"errors"
)

const (
	auFlagKey    = 1 << 0
	auFlagConfig = 1 << 1
	auHeaderLen  = 1 + 4 + 4 + 8 + 8 + 8
)

// AU is one encoded access unit (Annex-B) of the video stream.
type AU struct {
	Key, HasConfig   bool
	Width, Height    uint32
	Seq              uint64
	CaptureMonoUS    uint64
	EncodeDoneMonoUS uint64
	Data             []byte
}

// Marshal returns the frame payload without the kind byte.
func (a AU) Marshal() []byte {
	b := make([]byte, auHeaderLen+len(a.Data))
	if a.Key {
		b[0] |= auFlagKey
	}
	if a.HasConfig {
		b[0] |= auFlagConfig
	}
	binary.BigEndian.PutUint32(b[1:], a.Width)
	binary.BigEndian.PutUint32(b[5:], a.Height)
	binary.BigEndian.PutUint64(b[9:], a.Seq)
	binary.BigEndian.PutUint64(b[17:], a.CaptureMonoUS)
	binary.BigEndian.PutUint64(b[25:], a.EncodeDoneMonoUS)
	copy(b[auHeaderLen:], a.Data)
	return b
}

// UnmarshalAU parses a payload produced by AU.Marshal. Data aliases p.
func UnmarshalAU(p []byte) (AU, error) {
	if len(p) < auHeaderLen {
		return AU{}, errors.New("wire: AU payload shorter than its 33-byte header")
	}
	return AU{
		Key:              p[0]&auFlagKey != 0,
		HasConfig:        p[0]&auFlagConfig != 0,
		Width:            binary.BigEndian.Uint32(p[1:]),
		Height:           binary.BigEndian.Uint32(p[5:]),
		Seq:              binary.BigEndian.Uint64(p[9:]),
		CaptureMonoUS:    binary.BigEndian.Uint64(p[17:]),
		EncodeDoneMonoUS: binary.BigEndian.Uint64(p[25:]),
		Data:             p[auHeaderLen:],
	}, nil
}

// VideoInfo is the body of a KindVideoInfo frame: the stream's encoding
// from the next access unit on.
type VideoInfo struct {
	Codec     string `json:"codec"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SrcWidth  int    `json:"src_width"`
	SrcHeight int    `json:"src_height"`
	FPS       int    `json:"fps"`
	Bitrate   int    `json:"bitrate"`
	Encoder   string `json:"encoder"`
	Hardware  bool   `json:"hardware"`
	Capture   string `json:"capture"`
	Display   int    `json:"display"`
}

// VideoStats is the body of a KindVideoStats frame, about the second
// before it.
type VideoStats struct {
	FPS          float64 `json:"fps"`
	Bitrate      int     `json:"bitrate"`
	Dropped      int     `json:"dropped"`
	EncodeMSP50  float64 `json:"encode_ms_p50"`
	EncodeMSP95  float64 `json:"encode_ms_p95"`
	CaptureMSP50 float64 `json:"capture_ms_p50"`
}

// VideoPaused is the body of a KindVideoPaused frame.
type VideoPaused struct {
	Reason string `json:"reason"`
}

// Reasons of a VideoPaused.
const (
	PausedNoViewer           = "no_viewer"
	PausedDisplayUnavailable = "display_unavailable"
	PausedSecureDesktop      = "secure_desktop"
	PausedSessionLocked      = "session_locked"
)

// VideoControl is the body of a KindClientControl frame of the video
// stream. BPS goes with ControlBitrate; MaxFPS and MaxWidth go with
// ControlConfig and mean what the stream header's max_fps and max_width
// mean.
type VideoControl struct {
	Op       string `json:"op"`
	BPS      int    `json:"bps,omitempty"`
	MaxFPS   int    `json:"max_fps,omitempty"`
	MaxWidth int    `json:"max_width,omitempty"`
}

// Ops of a VideoControl.
const (
	ControlKeyframe = "keyframe"
	ControlBitrate  = "bitrate"
	ControlConfig   = "config"
	ControlPause    = "pause"
	ControlResume   = "resume"
)
