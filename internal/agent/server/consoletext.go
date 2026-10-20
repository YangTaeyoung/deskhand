package server

import (
	"io"
	"unicode/utf8"
)

// consoleText is the writer a command's output goes through: writes that
// hold UTF-8 pass on to w unchanged, other writes are converted to UTF-8
// with decode. The bytes of a character that a write cuts off wait for the
// next write or for flush.
type consoleText struct {
	w io.Writer
	// decode converts the longest prefix of b that holds whole characters
	// of the console code page and returns the UTF-8 text and the prefix
	// length.
	decode  func(b []byte) (text []byte, used int)
	pending []byte
}

func (c *consoleText) Write(p []byte) (int, error) {
	buf := append(c.pending, p...)
	var out []byte
	used := completeUTF8Len(buf)
	if utf8.Valid(buf[:used]) {
		out = buf[:used]
	} else {
		out, used = c.decode(buf)
	}
	if len(out) > 0 {
		if _, err := c.w.Write(out); err != nil {
			return 0, err
		}
	}
	c.pending = append([]byte(nil), buf[used:]...)
	return len(p), nil
}

// flush writes the bytes that wait for the rest of their character, with
// invalid UTF-8 replaced.
func (c *consoleText) flush() {
	if len(c.pending) > 0 {
		_, _ = c.w.Write([]byte(text(c.pending)))
		c.pending = nil
	}
}

// consoleOutput returns the writers a command's stdout and stderr go
// through, converting with decode when it is not nil, and the function that
// flushes them after the command ends.
func consoleOutput(decode func([]byte) ([]byte, int), stdout, stderr io.Writer) (io.Writer, io.Writer, func()) {
	if decode == nil {
		return stdout, stderr, func() {}
	}
	o, e := &consoleText{w: stdout, decode: decode}, &consoleText{w: stderr, decode: decode}
	return o, e, func() { o.flush(); e.flush() }
}
