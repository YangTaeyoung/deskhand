package wire

import (
	"bufio"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestHeaderRoundTrip(t *testing.T) {
	in := StreamHeader{
		S: "video", Display: 1, MaxFPS: 60, Bitrate: 8000000, MaxWidth: 1920, Codec: "h264",
		Gen: 7, Job: "j-1",
		New:  &PTYNew{Shell: "pwsh", Cwd: `C:\`, Cols: 120, Rows: 30, Env: map[string]string{"A": "b"}},
		Port: 8080, TransferID: "t-1", Path: `C:\x.bin`, Offset: 10, Size: 100, Length: 50,
		MTime: "2026-10-20T00:00:00Z",
	}
	var buf bytes.Buffer
	if err := WriteHeader(&buf, in); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), "\n") || strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("header must be one line: %q", buf.String())
	}
	var out StreamHeader
	if err := ReadHeader(bufio.NewReader(&buf), &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("got %+v want %+v", out, in)
	}
}

func TestHeaderJSONKeys(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteHeader(&buf, StreamHeader{S: "put", TransferID: "t", MaxFPS: 30, MaxWidth: 1}); err != nil {
		t.Fatal(err)
	}
	want := `{"s":"put","max_fps":30,"max_width":1,"transfer_id":"t"}` + "\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestHeaderTooLarge(t *testing.T) {
	line := `{"s":"` + strings.Repeat("a", MaxHeader) + `"}` + "\n"
	var out StreamHeader
	err := ReadHeader(bufio.NewReaderSize(strings.NewReader(line), 16), &out)
	if !errors.Is(err, ErrHeaderTooLarge) {
		t.Fatalf("err = %v", err)
	}
	err = WriteHeader(&bytes.Buffer{}, StreamHeader{S: strings.Repeat("a", MaxHeader)})
	if !errors.Is(err, ErrHeaderTooLarge) {
		t.Fatalf("write err = %v", err)
	}
}

func TestReadHeaderLeavesBodyUnread(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("{\"s\":\"video\"}\nBODY"))
	var h StreamHeader
	if err := ReadHeader(br, &h); err != nil {
		t.Fatal(err)
	}
	rest := make([]byte, 4)
	if _, err := br.Read(rest); err != nil || string(rest) != "BODY" {
		t.Fatalf("rest=%q err=%v", rest, err)
	}
}

func TestReadHeaderRejectsMissingNewline(t *testing.T) {
	var h StreamHeader
	if err := ReadHeader(bufio.NewReader(strings.NewReader(`{"s":"video"}`)), &h); err == nil {
		t.Fatal("expected error")
	}
}
