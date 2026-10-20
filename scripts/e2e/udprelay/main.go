// udprelay carries UDP datagrams between a UDP port on the Mac and a UDP
// address on the PC over the standard input and output of one ssh session
// (scripts/e2e/phase3.sh).
//
//	udprelay mac -listen 127.0.0.1:P [-drop 12s] [-log FILE] -- ssh … udprelay.exe pc -agent 127.0.0.1:A
//	udprelay pc -agent 127.0.0.1:A
//
// mac listens for datagrams on -listen, starts the command after "--" and
// writes each datagram to its standard input as a frame; frames read from
// its standard output go back to the sender of that flow. The command is
// started again a second after it ends, with no flows. SIGUSR1 drops every
// datagram in both directions for -drop; SIGINT and SIGTERM end the relay
// and the command.
//
// pc reads frames on standard input and sends each flow's datagrams to
// -agent from a UDP socket of the flow's own, framing the answers on
// standard output. It ends when standard input ends.
//
// A frame is a 4-byte big-endian flow number, a 2-byte big-endian length
// and that many bytes of the datagram; length 0 closes the flow.
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

const (
	// maxDatagram is the largest datagram the relay carries.
	maxDatagram = 65535
	// flowIdle is how long a flow may carry nothing before mac closes it.
	flowIdle = 5 * time.Minute
	// socketBuffer is the receive and send buffer asked for each UDP socket.
	socketBuffer = 8 << 20
	// queueLen is the number of frames waiting for the frame writer.
	queueLen = 4096
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: udprelay mac -listen ADDR -- COMMAND… | udprelay pc -agent ADDR")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "mac":
		err = runMac(os.Args[2:])
	case "pc":
		err = runPC(os.Args[2:])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "udprelay:", err)
		os.Exit(1)
	}
}

// frame is one datagram of a flow; an empty data closes the flow.
type frame struct {
	flow uint32
	data []byte
}

// frameWriter writes frames to w from a queue, flushing when the queue is
// empty.
type frameWriter struct {
	q        chan frame
	quit     chan struct{}
	done     chan struct{}
	quitOnce sync.Once
}

func newFrameWriter(w io.Writer) *frameWriter {
	fw := &frameWriter{q: make(chan frame, queueLen), quit: make(chan struct{}), done: make(chan struct{})}
	go fw.loop(bufio.NewWriterSize(w, 256<<10))
	return fw
}

func (fw *frameWriter) loop(bw *bufio.Writer) {
	defer close(fw.done)
	var hdr [6]byte
	write := func(f frame) error {
		binary.BigEndian.PutUint32(hdr[0:4], f.flow)
		binary.BigEndian.PutUint16(hdr[4:6], uint16(len(f.data)))
		if _, err := bw.Write(hdr[:]); err != nil {
			return err
		}
		_, err := bw.Write(f.data)
		return err
	}
	for {
		var f frame
		select {
		case f = <-fw.q:
		case <-fw.quit:
			for {
				select {
				case f = <-fw.q:
					if write(f) != nil {
						return
					}
				default:
					_ = bw.Flush()
					return
				}
			}
		}
		if write(f) != nil {
			return
		}
	more:
		for {
			select {
			case f = <-fw.q:
				if write(f) != nil {
					return
				}
			default:
				break more
			}
		}
		if bw.Flush() != nil {
			return
		}
	}
}

// send queues f unless the writer has stopped; it reports false then.
func (fw *frameWriter) send(f frame) bool {
	select {
	case <-fw.done:
		return false
	case <-fw.quit:
		return false
	case fw.q <- f:
		return true
	}
}

// close stops the writer once the queued frames are written.
func (fw *frameWriter) close() {
	fw.quitOnce.Do(func() { close(fw.quit) })
	<-fw.done
}

// readFrame reads one frame from r into buf.
func readFrame(r io.Reader, buf []byte) (uint32, []byte, error) {
	var hdr [6]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[4:6]))
	if _, err := io.ReadFull(r, buf[:n]); err != nil {
		return 0, nil, err
	}
	return binary.BigEndian.Uint32(hdr[0:4]), buf[:n], nil
}

func runPC(args []string) error {
	fs := flag.NewFlagSet("pc", flag.ContinueOnError)
	agent := fs.String("agent", "127.0.0.1:47650", "UDP address of the agent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raddr, err := net.ResolveUDPAddr("udp", *agent)
	if err != nil {
		return err
	}
	return servePC(raddr, os.Stdin, os.Stdout)
}

// servePC relays the frames of r to raddr and the answers to w until r
// ends.
func servePC(raddr *net.UDPAddr, r io.Reader, w io.Writer) error {
	out := newFrameWriter(w)
	defer out.close()
	flows := map[uint32]*net.UDPConn{}
	defer func() {
		for _, c := range flows {
			c.Close()
		}
	}()
	in := bufio.NewReaderSize(r, 256<<10)
	buf := make([]byte, maxDatagram)
	for {
		id, data, err := readFrame(in, buf)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		c := flows[id]
		if len(data) == 0 {
			if c != nil {
				c.Close()
				delete(flows, id)
			}
			continue
		}
		if c == nil {
			if c, err = net.DialUDP("udp", nil, raddr); err != nil {
				return err
			}
			_ = c.SetReadBuffer(socketBuffer)
			_ = c.SetWriteBuffer(socketBuffer)
			flows[id] = c
			go func(id uint32, c *net.UDPConn) {
				b := make([]byte, maxDatagram)
				for {
					n, err := c.Read(b)
					if err != nil {
						if isRefused(err) {
							continue
						}
						return
					}
					if n == 0 {
						continue
					}
					if !out.send(frame{id, append([]byte(nil), b[:n]...)}) {
						return
					}
				}
			}(id, c)
		}
		_, _ = c.Write(data)
	}
}

// isRefused reports whether err is the ICMP port-unreachable error a
// connected UDP socket returns while nothing listens on its peer.
func isRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.Errno(10054))
}
