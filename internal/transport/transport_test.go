package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newID(t *testing.T, cn string) Identity {
	t.Helper()
	id, err := NewIdentity(cn)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMutualTLSConnects(t *testing.T) {
	srvID, cliID := newID(t, "agent"), newID(t, "daemon")
	ln, err := Listen("127.0.0.1:0", srvID, func(fp string) bool { return fp == cliID.Fingerprint })
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := ln.Accept(ctx)
		if err != nil {
			done <- err
			return
		}
		st, err := conn.AcceptStream(ctx)
		if err != nil {
			done <- err
			return
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(st, buf); err != nil {
			done <- err
			return
		}
		if string(buf) != "ping" {
			done <- errors.New("unexpected payload " + string(buf))
			return
		}
		_, err = st.Write([]byte("pong"))
		done <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := Dial(ctx, ln.Addr().String(), cliID, srvID.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "")
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(st, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "pong" {
		t.Fatalf("got %q", buf)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPinnedMismatchRejected(t *testing.T) {
	srvID, cliID, other := newID(t, "agent"), newID(t, "daemon"), newID(t, "other")
	ln, err := Listen("127.0.0.1:0", srvID, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := Dial(ctx, ln.Addr().String(), cliID, other.Fingerprint)
	if conn != nil {
		conn.CloseWithError(0, "")
	}
	if !errors.Is(err, ErrPeerMismatch) {
		t.Fatalf("want ErrPeerMismatch, got %v", err)
	}
}

func TestUnknownClientRejected(t *testing.T) {
	srvID, cliID := newID(t, "agent"), newID(t, "daemon")
	ln, err := Listen("127.0.0.1:0", srvID, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := Dial(ctx, ln.Addr().String(), cliID, srvID.Fingerprint)
	if err == nil {
		defer conn.CloseWithError(0, "")
		select {
		case <-conn.Context().Done():
		case <-ctx.Done():
			t.Fatal("connection to a server that rejects this client stayed open")
		}
	}

	acceptCtx, acceptCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer acceptCancel()
	if c, err := ln.Accept(acceptCtx); err == nil {
		c.CloseWithError(0, "")
		t.Fatal("listener accepted a connection from an unknown client")
	}
}

func TestIdentitySaveLoad(t *testing.T) {
	id := newID(t, "agent")
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	if err := id.Save(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(keyPath)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("key mode %v", st.Mode().Perm())
		}
		st, _ = os.Stat(certPath)
		if st.Mode().Perm() != 0o644 {
			t.Fatalf("cert mode %v", st.Mode().Perm())
		}
	}
	loaded, err := LoadIdentity(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint != id.Fingerprint {
		t.Fatalf("fingerprint %s != %s", loaded.Fingerprint, id.Fingerprint)
	}
	if len(id.Fingerprint) != 64 {
		t.Fatalf("fingerprint length %d", len(id.Fingerprint))
	}
}

func TestFingerprintIsLowercaseHexSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte("abc"))
	if got := Fingerprint([]byte("abc")); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("got %s", got)
	}
}
