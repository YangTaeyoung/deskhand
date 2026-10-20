package server

import (
	"bytes"
	"testing"
)

// testCodePage decodes a two-byte code page that knows 안 (BE C8) and
// 녕 (B3 E7), with every byte from 0x81 a lead byte.
func testCodePage(b []byte) ([]byte, int) {
	chars := map[string]string{"\xbe\xc8": "안", "\xb3\xe7": "녕"}
	var out []byte
	n := 0
	for n < len(b) {
		if b[n] < 0x81 {
			out = append(out, b[n])
			n++
			continue
		}
		if n+1 >= len(b) {
			break
		}
		c, ok := chars[string(b[n:n+2])]
		if !ok {
			c = "?"
		}
		out = append(out, c...)
		n += 2
	}
	return out, n
}

func writeAll(t *testing.T, w *consoleText, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if n, err := w.Write([]byte(p)); err != nil || n != len(p) {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}
}

func TestConsoleTextPassesUTF8Through(t *testing.T) {
	var out bytes.Buffer
	w := &consoleText{w: &out, decode: testCodePage}
	s := "안녕 😀 ok\r\n"
	writeAll(t, w, s[:1], s[1:5], s[5:9], s[9:])
	w.flush()
	if out.String() != s {
		t.Fatalf("out = %q", out.String())
	}
}

func TestConsoleTextConvertsCodePageOutput(t *testing.T) {
	var out bytes.Buffer
	w := &consoleText{w: &out, decode: testCodePage}
	writeAll(t, w, "\xbe\xc8\xb3\xe7 \r\n")
	w.flush()
	if out.String() != "안녕 \r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestConsoleTextJoinsCharacterSplitAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	w := &consoleText{w: &out, decode: testCodePage}
	writeAll(t, w, "a\xbe", "\xc8\xb3", "\xe7\r\n")
	w.flush()
	if out.String() != "a안녕\r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestConsoleTextFlushReplacesUnfinishedCharacter(t *testing.T) {
	var out bytes.Buffer
	w := &consoleText{w: &out, decode: testCodePage}
	writeAll(t, w, "x\xbe\xc8\xb3")
	if out.String() != "x안" {
		t.Fatalf("before flush out = %q", out.String())
	}
	w.flush()
	if out.String() != "x안�" {
		t.Fatalf("after flush out = %q", out.String())
	}
}

func TestConsoleOutputWithoutDecoderWritesDirectly(t *testing.T) {
	var out, errOut bytes.Buffer
	stdout, stderr, flush := consoleOutput(nil, &out, &errOut)
	if stdout != &out || stderr != &errOut {
		t.Fatalf("writers were wrapped without a decoder")
	}
	flush()
}
