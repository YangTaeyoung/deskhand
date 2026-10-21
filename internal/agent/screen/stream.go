package screen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// maxControlFrame bounds a frame the daemon sends on the stream.
	maxControlFrame = 64 << 10
	// writeTimeout bounds the write of one frame.
	writeTimeout = 10 * time.Second
	// refusalTimeout bounds the write of a refusal line.
	refusalTimeout = 5 * time.Second
)

// Stream is the agent's end of a video stream after its header line.
// Close ends both directions.
type Stream interface {
	io.Reader
	io.Writer
	SetWriteDeadline(t time.Time) error
	Close() error
}

// refusal is the line written to a refused stream.
type refusal struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

// ServeVideo serves the video stream st with header h (docs/protocol.md
// §2, §4): a pipeline writes its frames and the control frames read from
// st steer it. It returns once ctx ends, the daemon ends its direction of
// the stream or a write fails, with the pipeline stopped and st closed. A
// header the pipeline refuses gets the refusal line instead of frames.
func ServeVideo(ctx context.Context, st Stream, h wire.StreamHeader, opt Options) {
	p := New(opt)
	lg := p.opt.Logger
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer cancel()
		readControls(st, p, lg)
	}()
	var mu sync.Mutex
	ended := false
	stop := context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		ended = true
		_ = st.SetWriteDeadline(time.Now())
	})
	err := p.Start(ctx, h, func(kind byte, payload []byte) error {
		mu.Lock()
		if ended {
			mu.Unlock()
			return net.ErrClosed
		}
		_ = st.SetWriteDeadline(time.Now().Add(writeTimeout))
		mu.Unlock()
		return wire.WriteFrame(st, kind, payload)
	})
	stop()
	var ref *Refusal
	switch {
	case errors.As(err, &ref):
		_ = st.SetWriteDeadline(time.Now().Add(refusalTimeout))
		_ = wire.WriteHeader(st, refusal{OK: false, Code: ref.Code, Error: ref.Message})
	case err != nil && ctx.Err() == nil:
		lg.Printf("video: the stream ended: %v", err)
	}
	_ = st.Close()
	<-readDone
}

// readControls hands the control frames of st to p until st ends.
func readControls(st io.Reader, p *Pipeline, lg *log.Logger) {
	for {
		kind, payload, err := wire.ReadFrame(st, maxControlFrame)
		if err != nil {
			return
		}
		if kind != wire.KindClientControl {
			lg.Printf("video: a frame of kind %#x on the video stream ignored", kind)
			continue
		}
		var c wire.VideoControl
		if err := json.Unmarshal(payload, &c); err != nil {
			lg.Printf("video: a control frame that is not JSON ignored: %v", err)
			continue
		}
		p.Control(c)
	}
}
