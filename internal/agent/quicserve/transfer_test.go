package quicserve

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// rpcCaller sends requests on an rpc stream and returns their answers.
type rpcCaller struct {
	t  *testing.T
	r  *proto.Reader
	w  *proto.Writer
	id int
}

func (c *rpcCaller) call(op string, fields proto.Message) proto.Message {
	c.t.Helper()
	c.id++
	req := proto.Message{"t": "req", "id": c.id, "op": op}
	for k, v := range fields {
		req[k] = v
	}
	if err := c.w.Send(req); err != nil {
		c.t.Fatal(err)
	}
	for {
		res := next(c.t, c.r, "res")
		if res.Int("id") == c.id {
			return res
		}
	}
}

// readJSONLine reads one JSON line of br.
func readJSONLine(t *testing.T, br *bufio.Reader) proto.Message {
	t.Helper()
	line, err := br.ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading a line: %v (read %q)", err, line)
	}
	var m proto.Message
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("line %q: %v", line, err)
	}
	return m
}

// putOver opens a put stream with header h on conn, sends body and its end,
// and returns the acceptance and result lines.
func putOver(t *testing.T, conn *quic.Conn, h wire.StreamHeader, body []byte) (proto.Message, proto.Message) {
	t.Helper()
	h.S = "put"
	st := openStream(t, conn, h)
	br := bufio.NewReader(st)
	accept := readJSONLine(t, br)
	if accept["ok"] != true {
		return accept, nil
	}
	if _, err := st.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	result := readJSONLine(t, br)
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("the put stream went on after its result: %v", err)
	}
	return accept, result
}

func TestPutAndGetStreamsOverQUIC(t *testing.T) {
	a := startAgentWith(t, TransferStreams)
	conn := a.dial(t)
	_, r, w := openRPC(t, conn)
	c := &rpcCaller{t: t, r: r, w: w}
	dst := filepath.Join(a.home, "sent.bin")
	data := make([]byte, 5<<20+3)
	rand.Read(data)
	sum := sha256.Sum256(data)
	mtime := time.Date(2024, 7, 8, 9, 10, 11, 123456700, time.UTC).Format(time.RFC3339Nano)
	h := wire.StreamHeader{TransferID: "tq", Path: dst, Size: int64(len(data)), MTime: mtime}

	if res := c.call("xfer.prepare", proto.Message{"transfer_id": "tq", "path": dst, "size": len(data), "mtime": mtime}); res["ok"] != true || res.Int("part_size") != 0 {
		t.Fatalf("prepare %v", res)
	}
	half := int64(len(data) / 2)
	accept, result := putOver(t, conn, h, data[:half])
	if accept["ok"] != true || accept.Int("part_size") != 0 || result["ok"] != true || result.Int("received") != int(half) || result.Int("part_size") != int(half) {
		t.Fatalf("first put %v %v", accept, result)
	}

	h.S = "put"
	got := readRefusal(t, openStream(t, conn, h))
	if got.OK || got.Code != wire.CodeInvalidArgument || !strings.Contains(got.Error, "offset 0") {
		t.Fatalf("a put at offset 0 after %d bytes answered %+v", half, got)
	}

	h.Offset = half
	if _, result := putOver(t, conn, h, data[half:]); result["ok"] != true || result.Int("part_size") != len(data) {
		t.Fatalf("second put %v", result)
	}
	res := c.call("xfer.commit", proto.Message{"transfer_id": "tq", "path": dst, "sha256": hex.EncodeToString(sum[:]), "rid": "r-tq"})
	if res["ok"] != true || res.String("outcome") != "written" {
		t.Fatalf("commit %v", res)
	}
	if b, err := os.ReadFile(dst); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("the committed file differs: %v", err)
	}

	st := openStream(t, conn, wire.StreamHeader{S: "get", Path: dst, Offset: 1000})
	br := bufio.NewReader(st)
	accept = readJSONLine(t, br)
	if accept["ok"] != true || accept.Int("size") != len(data) || accept.Int("length") != len(data)-1000 || accept.String("mtime") != mtime {
		t.Fatalf("get answered %v", accept)
	}
	rest, err := io.ReadAll(br)
	if err != nil || !bytes.Equal(rest, data[1000:]) {
		t.Fatalf("get carried %d bytes: %v", len(rest), err)
	}

	got = readRefusal(t, openStream(t, conn, wire.StreamHeader{S: "get", Path: filepath.Join(a.home, "missing.bin")}))
	if got.OK || got.Code != wire.CodeNotFound {
		t.Fatalf("get of a missing file answered %+v", got)
	}
	if res := c.call("ping", nil); res["ok"] != true {
		t.Fatalf("the rpc stream after the transfer streams answered %v", res)
	}
}
