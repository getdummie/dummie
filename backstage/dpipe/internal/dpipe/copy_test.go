package dpipe

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// connPair returns the two ends of a connected unix socket pair as net.Conns.
func connPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	return adoptConn(t, fds[0]), adoptConn(t, fds[1])
}

func adoptConn(t *testing.T, fd int) net.Conn {
	t.Helper()
	f := os.NewFile(uintptr(fd), "socketpair")
	defer f.Close()
	c, err := net.FileConn(f)
	if err != nil {
		t.Fatalf("FileConn: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestPipeHalfClose(t *testing.T) {
	clientNear, clientFar := connPair(t)
	backendNear, backendFar := connPair(t)

	done := make(chan struct{})
	go func() {
		Pipe(clientFar, backendFar)
		close(done)
	}()

	if _, err := clientNear.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(backendNear, buf); err != nil {
		t.Fatalf("read on backend: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("backend got %q", buf)
	}

	// Client half-closes: the backend must see EOF but still be able to reply.
	if err := clientNear.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	if _, err := backendNear.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("backend read after half-close: err = %v, want EOF", err)
	}

	if _, err := backendNear.Write([]byte("pong")); err != nil {
		t.Fatalf("backend write: %v", err)
	}
	if _, err := io.ReadFull(clientNear, buf); err != nil {
		t.Fatalf("read on client: %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("client got %q", buf)
	}

	if err := backendNear.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatalf("backend CloseWrite: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Pipe did not return after both halves closed")
	}
}
