package screen

import "time"

const (
	// warmWindow is how long after an injected input the last image is
	// encoded again while the screen does not change.
	warmWindow = 750 * time.Millisecond
	// warmMaxFPS caps the rate of those frames.
	warmMaxFPS = 60
	// actionHoldOff is how long after an input other than a pointer move
	// no image is encoded again.
	actionHoldOff = 50 * time.Millisecond
)

// warmState is what warmDue decides on, in Clock readings (0 for never):
// the last injected input, the last one other than a pointer move, the
// last new image taken from the source and the last frame given to the
// encoder; the stream's max_fps and the frames in the encoder.
type warmState struct {
	inputUS, actionUS, imageUS, auUS uint64
	maxFPS, inFlight                 int
}

// warmInterval is the least time in µs between the frames given to the
// encoder that warmDue allows at maxFPS: one frame interval at the lower
// of maxFPS (at least 1) and warmMaxFPS, rounded up.
func warmInterval(maxFPS int) uint64 {
	fps := uint64(max(1, min(maxFPS, warmMaxFPS)))
	return (uint64(time.Second/time.Microsecond) + fps - 1) / fps
}

// warmDue reports whether the last image is to be encoded again at nowUS
// to keep the encoder working: an input was injected less than warmWindow
// before nowUS and none other than a pointer move within actionHoldOff,
// the encoder holds none of the frames given to it, the last of them went
// one warmInterval or more before nowUS, and the last new image came one
// and a quarter warmInterval or more before it.
func warmDue(s warmState, nowUS uint64) bool {
	if s.inputUS == 0 || s.maxFPS <= 0 || s.inFlight > 0 {
		return false
	}
	interval := warmInterval(s.maxFPS)
	switch {
	case nowUS >= s.inputUS+uint64(warmWindow/time.Microsecond):
		return false
	case s.actionUS != 0 && nowUS < s.actionUS+uint64(actionHoldOff/time.Microsecond):
		return false
	case nowUS < s.auUS+interval:
		return false
	case s.imageUS != 0 && nowUS < s.imageUS+interval+interval/4:
		return false
	}
	return true
}

// earliestUS is the first reading at which warmDue may hold for s, the
// frames in the encoder and the window aside.
func (s warmState) earliestUS() uint64 {
	interval := warmInterval(s.maxFPS)
	t := s.auUS + interval
	if s.imageUS != 0 {
		t = max(t, s.imageUS+interval+interval/4)
	}
	if s.actionUS != 0 {
		t = max(t, s.actionUS+uint64(actionHoldOff/time.Microsecond))
	}
	return t
}
