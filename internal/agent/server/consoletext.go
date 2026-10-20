package server

import (
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// consoleText is the writer a command's output goes through: writes that
// hold UTF-8 pass on to w unchanged, other writes are converted to UTF-8
// with decode. Once a write has held valid multi-byte UTF-8 the stream is
// taken as UTF-8 and every later write passes on unchanged. The bytes of a
// character that a write cuts off wait for the next write or for flush.
type consoleText struct {
	w io.Writer
	// decode converts the longest prefix of b that holds whole characters
	// of the console code page and returns the UTF-8 text and the prefix
	// length.
	decode  func(b []byte) (text []byte, used int)
	pending []byte
	// utf8 is set once a write held valid multi-byte UTF-8.
	utf8 bool
}

func (c *consoleText) Write(p []byte) (int, error) {
	buf := append(c.pending, p...)
	var out []byte
	used := completeUTF8Len(buf)
	switch {
	case c.utf8:
		out = buf[:used]
	case utf8.Valid(buf[:used]):
		out = buf[:used]
		c.utf8 = hasMultiByte(out)
	default:
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

// hasMultiByte reports whether b holds a byte outside ASCII.
func hasMultiByte(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// flush writes the bytes that wait for the rest of their character, with
// invalid UTF-8 replaced.
func (c *consoleText) flush() {
	if len(c.pending) > 0 {
		_, _ = c.w.Write([]byte(text(c.pending)))
		c.pending = nil
	}
}

// decodeDBCS converts the longest prefix of b that holds whole characters of
// a double-byte code page to UTF-8 and returns it with the prefix length.
// lead reports a lead byte of the code page; convert turns the prefix into
// UTF-16 and reports whether it could. When convert fails the prefix is
// passed on with invalid UTF-8 replaced.
func decodeDBCS(b []byte, lead func(byte) bool, convert func([]byte) ([]uint16, bool)) ([]byte, int) {
	n := 0
	for n < len(b) {
		if lead(b[n]) {
			if n+1 >= len(b) {
				break
			}
			n += 2
			continue
		}
		n++
	}
	if n == 0 {
		return nil, 0
	}
	wide, ok := convert(b[:n])
	if !ok {
		return []byte(strings.ToValidUTF8(string(b[:n]), "\uFFFD")), n
	}
	return []byte(string(utf16.Decode(wide))), n
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
