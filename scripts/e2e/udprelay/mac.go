//go:build !windows

package main

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// macRelay is the state of mac mode.
type macRelay struct {
	ln      *net.UDPConn
	dropFor time.Duration
	logger  *log.Logger

	dropUntil atomic.Int64

	mu    sync.Mutex
	out   *frameWriter
	next  uint32
	byKey map[string]*macFlow
	byID  map[uint32]*macFlow
}

type macFlow struct {
	id   uint32
	addr *net.UDPAddr
	last atomic.Int64
}

func (m *macRelay) dropping() bool { return time.Now().UnixNano() < m.dropUntil.Load() }

func runMac(args []string) error {
	fs := flag.NewFlagSet("mac", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:47651", "UDP address the daemon dials")
	drop := fs.Duration("drop", 12*time.Second, "how long SIGUSR1 drops datagrams")
	logPath := fs.String("log", "", "log file (standard error when empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		return errors.New("no command after --")
	}
	lw := io.Writer(os.Stderr)
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		lw = f
	}
	laddr, err := net.ResolveUDPAddr("udp", *listen)
	if err != nil {
		return err
	}
	ln, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	defer ln.Close()
	_ = ln.SetReadBuffer(socketBuffer)
	_ = ln.SetWriteBuffer(socketBuffer)
	m := &macRelay{ln: ln, dropFor: *drop, logger: log.New(lw, "", log.LstdFlags|log.Lmicroseconds),
		byKey: map[string]*macFlow{}, byID: map[uint32]*macFlow{}}
	m.logger.Printf("listening on udp %s", ln.LocalAddr())

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGUSR1, syscall.SIGINT, syscall.SIGTERM)
	stop := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		for s := range sigs {
			if s == syscall.SIGUSR1 {
				m.dropUntil.Store(time.Now().Add(m.dropFor).UnixNano())
				m.logger.Printf("dropping datagrams for %s", m.dropFor)
				continue
			}
			m.logger.Printf("%s: stopping", s)
			stopOnce.Do(func() { close(stop) })
			return
		}
	}()
	go m.readUDP()
	go m.expire(stop)

	for {
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.Stderr = lw
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		m.logger.Printf("command started, pid %d", cmd.Process.Pid)
		out := newFrameWriter(stdin)
		m.mu.Lock()
		m.out = out
		m.byKey, m.byID = map[string]*macFlow{}, map[uint32]*macFlow{}
		m.mu.Unlock()
		ended := make(chan error, 1)
		go func() {
			m.readCommand(stdout)
			ended <- cmd.Wait()
		}()
		select {
		case <-stop:
			_ = stdin.Close()
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-ended:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-ended
			}
			return nil
		case err := <-ended:
			m.logger.Printf("command ended: %v; starting it again in 1s", err)
		}
		m.mu.Lock()
		m.out = nil
		m.mu.Unlock()
		out.close()
		select {
		case <-stop:
			return nil
		case <-time.After(time.Second):
		}
	}
}

// readUDP frames each datagram from the daemon side onto the command's
// standard input.
func (m *macRelay) readUDP() {
	buf := make([]byte, maxDatagram)
	for {
		n, addr, err := m.ln.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if n == 0 || m.dropping() {
			continue
		}
		m.mu.Lock()
		out := m.out
		f := m.byKey[addr.String()]
		if f == nil && out != nil {
			m.next++
			f = &macFlow{id: m.next, addr: addr}
			m.byKey[addr.String()], m.byID[f.id] = f, f
			m.logger.Printf("flow %d from %s", f.id, addr)
		}
		m.mu.Unlock()
		if out == nil {
			continue
		}
		f.last.Store(time.Now().UnixNano())
		out.send(frame{f.id, append([]byte(nil), buf[:n]...)})
	}
}

// readCommand sends each frame of the command's standard output to its
// flow's address until the output ends.
func (m *macRelay) readCommand(r io.Reader) {
	br := bufio.NewReaderSize(r, 256<<10)
	buf := make([]byte, maxDatagram)
	for {
		id, data, err := readFrame(br, buf)
		if err != nil {
			return
		}
		if len(data) == 0 || m.dropping() {
			continue
		}
		m.mu.Lock()
		f := m.byID[id]
		m.mu.Unlock()
		if f == nil {
			continue
		}
		f.last.Store(time.Now().UnixNano())
		_, _ = m.ln.WriteToUDP(data, f.addr)
	}
}

// expire closes flows that carried nothing for flowIdle.
func (m *macRelay) expire(stop <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		cut := time.Now().Add(-flowIdle).UnixNano()
		m.mu.Lock()
		for k, f := range m.byKey {
			if f.last.Load() < cut {
				delete(m.byKey, k)
				delete(m.byID, f.id)
				if m.out != nil {
					m.out.send(frame{flow: f.id})
				}
				m.logger.Printf("flow %d idle, closed", f.id)
			}
		}
		m.mu.Unlock()
	}
}
