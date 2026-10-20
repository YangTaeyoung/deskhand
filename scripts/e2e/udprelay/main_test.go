package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestServePCRelaysEachFlowFromItsOwnSocket(t *testing.T) {
	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, maxDatagram)
		for {
			n, addr, err := echo.ReadFromUDP(b)
			if err != nil {
				return
			}
			_, _ = echo.WriteToUDP(append([]byte(addr.String()+" "), b[:n]...), addr)
		}
	}()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- servePC(echo.LocalAddr().(*net.UDPAddr), inR, outW) }()
	fw := newFrameWriter(inW)
	fw.send(frame{1, []byte("one")})
	fw.send(frame{2, []byte("two")})

	got := map[uint32][]byte{}
	buf := make([]byte, maxDatagram)
	for len(got) < 2 {
		id, data, err := readFrame(outR, buf)
		if err != nil {
			t.Fatal(err)
		}
		got[id] = append([]byte(nil), data...)
	}
	a, b := bytes.Fields(got[1]), bytes.Fields(got[2])
	if len(a) != 2 || string(a[1]) != "one" || len(b) != 2 || string(b[1]) != "two" {
		t.Fatalf("answers %q %q, want the echo of one and two", got[1], got[2])
	}
	if bytes.Equal(a[0], b[0]) {
		t.Fatalf("both flows reached the server from %s, want two sockets", a[0])
	}
	fw.close()
	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("servePC did not end after its input ended")
	}
}
