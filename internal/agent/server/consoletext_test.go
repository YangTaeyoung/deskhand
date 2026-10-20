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

func TestConsoleTextKeepsAUTF8StreamThroughAnInvalidByte(t *testing.T) {
	var out bytes.Buffer
	w := &consoleText{w: &out, decode: testCodePage}
	writeAll(t, w, "안녕\n", "a\xffb 😀\n")
	w.flush()
	if got := text(out.Bytes()); got != "안녕\na�b 😀\n" {
		t.Fatalf("out = %q", got)
	}
}

func TestConsoleTextASCIIDoesNotDecideTheEncoding(t *testing.T) {
	var out bytes.Buffer
	w := &consoleText{w: &out, decode: testCodePage}
	writeAll(t, w, "ok\r\n", "\xbe\xc8\r\n")
	w.flush()
	if out.String() != "ok\r\n안\r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestDecodeDBCSConvertsWholeCharacters(t *testing.T) {
	lead := func(c byte) bool { return c >= 0x81 }
	convert := func(p []byte) ([]uint16, bool) {
		if string(p) != "a\xbe\xc8" {
			t.Fatalf("convert got % x", p)
		}
		return []uint16{'a', 0xC548}, true
	}
	got, used := decodeDBCS([]byte("a\xbe\xc8\xb3"), lead, convert)
	if used != 3 || string(got) != "a안" {
		t.Fatalf("decodeDBCS = %q, %d", got, used)
	}
}

func TestDecodeDBCSFallsBackWhenConversionFails(t *testing.T) {
	lead := func(c byte) bool { return c >= 0x81 }
	fail := func([]byte) ([]uint16, bool) { return nil, false }
	got, used := decodeDBCS([]byte("a\xbe\xc8\xb3"), lead, fail)
	if used != 3 || string(got) != "a�" {
		t.Fatalf("decodeDBCS = %q, %d", got, used)
	}
	if got, used := decodeDBCS([]byte("\xbe"), lead, fail); used != 0 || len(got) != 0 {
		t.Fatalf("a lone lead byte gave %q, %d", got, used)
	}
}
