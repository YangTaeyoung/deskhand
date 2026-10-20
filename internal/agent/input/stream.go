package input

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// maxEventFrame bounds a frame the daemon sends on an input stream.
	maxEventFrame = 4 << 20
	// queuedEvents bounds the events read ahead of the one being injected.
	queuedEvents = 256
	// writeTimeout bounds the write of one line or ack.
	writeTimeout = 10 * time.Second
)

// Stream is the agent's end of an input stream after its header line.
// Close ends both directions.
type Stream interface {
	io.Reader
	io.Writer
	SetWriteDeadline(t time.Time) error
	Close() error
}

// streamLine is the first line the agent writes on an input stream.
type streamLine struct {
	OK    bool   `json:"ok"`
	Gen   uint64 `json:"gen,omitempty"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// queued is an event read from the stream, or the ack of a frame that
// carried no event.
type queued struct {
	ev  wire.InputEvent
	ack *wire.InputAck
}

// ServeStream serves the input stream st with header h (docs/protocol.md
// §2, §6). A header of an older writer generation is refused with
// CONTROL_PREEMPTED, a newer one becomes the writer generation; otherwise
// the agent writes {"ok":true,"gen":…} and then injects the 0x41 events in
// order and acks each with a 0x42 frame. An event of a newer generation
// raises the writer generation as soon as it is read, before the events
// queued ahead of it. When the daemon ends its direction the queued events
// are still injected; when ctx ends or a write fails they are dropped.
// Either way the keys and buttons the stream holds are then released and
// st is closed.
func (j *Injector) ServeStream(ctx context.Context, st Stream, h wire.StreamHeader) {
	lg := j.opt.Logger
	defer st.Close()
	refuse := func(e *Error) {
		_ = st.SetWriteDeadline(time.Now().Add(writeTimeout))
		_ = wire.WriteHeader(st, streamLine{Code: e.Code, Error: e.Msg})
	}
	if j.sys == nil {
		refuse(errUnsupported())
		return
	}
	if err := j.Admit(h.Gen); err != nil {
		var e *Error
		errors.As(err, &e)
		refuse(e)
		return
	}
	_ = st.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := wire.WriteHeader(st, streamLine{OK: true, Gen: j.Gen()}); err != nil {
		return
	}
	owner := j.owners.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopClosing := context.AfterFunc(ctx, func() { _ = st.Close() })
	defer stopClosing()

	queue := make(chan queued, queuedEvents)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(queue)
		if err := j.readEvents(ctx, st, queue); err != nil && ctx.Err() == nil {
			lg.Printf("input: the stream ended: %v", err)
			cancel()
		}
	}()
	for q := range queue {
		if ctx.Err() != nil {
			continue
		}
		ack := q.ack
		if ack == nil {
			a := j.apply(owner, q.ev)
			ack = &a
		}
		b, _ := json.Marshal(ack)
		_ = st.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := wire.WriteFrame(st, wire.KindInputAck, b); err != nil {
			if ctx.Err() == nil {
				lg.Printf("input: writing an ack: %v", err)
			}
			cancel()
		}
	}
	<-readDone
	if released, err := j.releaseOwner(owner); err != nil {
		lg.Printf("input: releasing the keys and buttons a closed stream held: %v", err)
	} else if len(released) > 0 {
		lg.Printf("input: released %d keys and buttons a closed stream held", len(released))
	}
}

// readEvents queues the events of st until it ends; it returns nil when
// the daemon ended its direction.
func (j *Injector) readEvents(ctx context.Context, st io.Reader, queue chan<- queued) error {
	for {
		kind, payload, err := wire.ReadFrame(st, maxEventFrame)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if kind != wire.KindClientInput {
			j.opt.Logger.Printf("input: a frame of kind %#x on the input stream ignored", kind)
			continue
		}
		var q queued
		if err := json.Unmarshal(payload, &q.ev); err != nil {
			var head struct {
				Seq uint64 `json:"seq"`
			}
			_ = json.Unmarshal(payload, &head)
			q.ack = &wire.InputAck{Seq: head.Seq, Code: wire.CodeInvalidArgument,
				Error: "the input frame is not a JSON event of docs/protocol.md §6; send {\"seq\":…,\"gen\":…,\"k\":…}"}
		} else if q.ev.Gen > j.Gen() {
			j.SetWriter(q.ev.Gen)
		}
		select {
		case queue <- q:
		case <-ctx.Done():
			return nil
		}
	}
}
