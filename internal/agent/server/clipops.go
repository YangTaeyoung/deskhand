package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (s *Server) registerClipboard() {
	s.register("clipboard.get", false, s.clipboardGet)
	s.register("clipboard.set", false, s.clipboardSet)
}

// clipboardGet answers the clipboard text, cut to clipboardMaxBytes.
func (s *Server) clipboardGet(context.Context, proto.Message) (proto.Message, error) {
	if !s.opt.Clipboard.Available() {
		return nil, errors.New(errNoClipboard)
	}
	text, err := s.opt.Clipboard.Text()
	if err != nil {
		return nil, fmt.Errorf("the clipboard could not be read (%v); retry once the PC's desktop answers", err)
	}
	return proto.Message{"text": cutUTF8Bytes(text, clipboardMaxBytes)}, nil
}

// clipboardSet replaces the clipboard with text; the watcher does not report
// the change as the PC's.
func (s *Server) clipboardSet(_ context.Context, req proto.Message) (proto.Message, error) {
	text, ok := req["text"].(string)
	if !ok {
		return nil, coded(wire.CodeInvalidArgument, "clipboard.set needs text, a string", false)
	}
	if !s.opt.Clipboard.Available() {
		return nil, errors.New(errNoClipboard)
	}
	if err := s.opt.Clipboard.SetText(text); err != nil {
		return nil, fmt.Errorf("the clipboard could not be set (%v); retry once the PC's desktop answers", err)
	}
	s.markOwnClipboard()
	return proto.Message{}, nil
}
