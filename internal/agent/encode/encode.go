// Package encode turns the frames of a capture.Source into H.264 access
// units with Media Foundation (docs/adr/0001-screen-pipeline.md): the
// D3D11 video processor converts each BGRA frame to NV12 at the output
// size, a hardware encoder MFT (asynchronous, D3D11 aware) encodes it, and
// the Microsoft software H.264 encoder MFT takes its place when no
// hardware encoder can be set up. Both run in low-latency mode with
// constant bitrate, no B frames and no periodic IDR, write Annex-B and put
// the SPS and PPS in every IDR access unit.
//
// An Encoder holds COM objects: create it on a goroutine locked to its
// thread that called InitThread, and call Encode, SetBitrate and Close
// there. Output may be read anywhere.
package encode

import (
	"errors"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Encoder encodes the frames of one source at one output size.
type Encoder interface {
	// Encode converts f, a frame of the source size given to New, to the
	// output size and queues it for encoding, as an IDR access unit when
	// forceKey is set. It returns ErrBusy when the encoder has no room for
	// a frame now; the frame is then dropped. Another error means the
	// encoder is broken and has to be made again.
	Encode(f capture.Frame, forceKey bool) error
	// Output delivers the access units in the order of their frames, with
	// Seq 0, CaptureMonoUS the frame's MonoUS and EncodeDoneMonoUS the
	// monoclock reading when the encoder returned it. Close closes it, and
	// so does a failure of the encoder.
	Output() <-chan wire.AU
	// SetBitrate changes the mean bitrate from the next frame on.
	SetBitrate(bps int) error
	Info() EncoderInfo
	Close()
}

// EncoderInfo names the encoder MFT and the size of its access units.
type EncoderInfo struct {
	Name          string
	Hardware      bool
	Width, Height int
}

var (
	// ErrBusy is the error of an Encode that found no room for the frame.
	ErrBusy = errors.New("the encoder has no room for another frame now")
	// ErrNoEncoder is wrapped by the error of New when neither a hardware
	// nor the software H.264 encoder can be set up, and of Probe.
	ErrNoEncoder = errors.New("no H.264 encoder can be set up on this PC; on Windows N install the Media Feature Pack")
	// ErrUnsupported is the error of New and Probe on a system other than
	// Windows.
	ErrUnsupported = errors.New("H.264 encoding needs a Windows agent; ask a Windows PC")
)

// NAL unit types of H.264.
const (
	nalIDR = 5
	nalSPS = 7
	nalPPS = 8
)

// NALTypes returns the nal_unit_type of each NAL unit of the Annex-B data
// b, in order.
func NALTypes(b []byte) []byte {
	var types []byte
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			types = append(types, b[i+3]&0x1f)
			i += 3
		}
	}
	return types
}

// describe reports whether the access unit b holds an IDR slice (key) and
// both an SPS and a PPS (config).
func describe(b []byte) (key, config bool) {
	var sps, pps bool
	for _, t := range NALTypes(b) {
		switch t {
		case nalIDR:
			key = true
		case nalSPS:
			sps = true
		case nalPPS:
			pps = true
		}
	}
	return key, sps && pps
}

// levels are the H.264 levels from 3.0 up (eAVEncH264VLevel, the level
// times ten) with their maximum frame size and macroblock rate and the
// maximum bitrate of the High profile.
var levels = []struct {
	level      uint32
	maxFS      int
	maxMBPS    int
	maxHighBPS int
}{
	{30, 1620, 40500, 12_500_000},
	{31, 3600, 108000, 17_500_000},
	{32, 5120, 216000, 25_000_000},
	{40, 8192, 245760, 25_000_000},
	{41, 8192, 245760, 62_500_000},
	{42, 8704, 522240, 62_500_000},
	{50, 22080, 589824, 168_750_000},
	{51, 36864, 983040, 300_000_000},
	{52, 36864, 2073600, 300_000_000},
}

// h264Level returns the lowest level from 3.0 to 5.2 that admits w×h at
// fps and bps in the High profile, or 0 when none does.
func h264Level(w, h, fps, bps int) uint32 {
	mbs := ((w + 15) / 16) * ((h + 15) / 16)
	for _, l := range levels {
		if mbs <= l.maxFS && mbs*fps <= l.maxMBPS && bps <= l.maxHighBPS {
			return l.level
		}
	}
	return 0
}

// checkSizes returns an error unless the source and output sizes are
// positive, the output is even and no larger than the source, and fps and
// bps are positive.
func checkSizes(srcW, srcH, outW, outH, fps, bps int) error {
	switch {
	case srcW <= 0 || srcH <= 0 || outW <= 0 || outH <= 0:
		return fmt.Errorf("encoder sizes %dx%d to %dx%d are not positive", srcW, srcH, outW, outH)
	case outW%2 != 0 || outH%2 != 0:
		return fmt.Errorf("the encoder output %dx%d is not even", outW, outH)
	case outW > srcW || outH > srcH:
		return fmt.Errorf("the encoder output %dx%d is larger than its source %dx%d", outW, outH, srcW, srcH)
	case fps <= 0 || bps <= 0:
		return fmt.Errorf("encoder frame rate %d and bitrate %d are not positive", fps, bps)
	}
	return nil
}

// sampleClock gives the frames of an encoder their sample times: 100 ns
// units since the capture of the first frame, each later than the one
// before.
type sampleClock struct {
	base    uint64
	last    int64
	started bool
}

// next returns the sample time of a frame captured at the monoclock
// reading mono.
func (c *sampleClock) next(mono uint64) int64 {
	if !c.started {
		c.base = mono
	}
	t := int64(0)
	if mono > c.base {
		t = int64(mono-c.base) * 10
	}
	if c.started && t <= c.last {
		t = c.last + 1
	}
	c.last, c.started = t, true
	return t
}

// mono returns the monoclock reading of the sample time t.
func (c *sampleClock) mono(t int64) uint64 { return c.base + uint64(max(t, 0)/10) }
