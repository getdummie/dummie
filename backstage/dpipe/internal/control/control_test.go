package control_test

import (
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"dpipe/internal/control"
)

func socketPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	return adoptUnix(t, fds[0]), adoptUnix(t, fds[1])
}

func adoptUnix(t *testing.T, fd int) *net.UnixConn {
	t.Helper()
	f := os.NewFile(uintptr(fd), "socketpair")
	defer f.Close()
	c, err := net.FileConn(f)
	if err != nil {
		t.Fatalf("FileConn: %v", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		t.Fatalf("not a unix conn: %T", c)
	}
	t.Cleanup(func() { _ = uc.Close() })
	return uc
}

func TestEncodeDecode(t *testing.T) {
	a, b := socketPair(t)
	ka, kb := control.NewConn(a), control.NewConn(b)

	want := control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: "abc123",
		Kind: control.KindSSH, SSHUser: "dev", SSHPubKey: "ssh-ed25519 AAAA",
		SSHFingerprint: "SHA256:xyz", ClientIP: "10.0.0.1",
	}
	if err := ka.SendMsg(want, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	got, fds, err := kb.RecvMsg()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if len(fds) != 0 {
		t.Fatalf("unexpected fds: %v", fds)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestOversizeRejected(t *testing.T) {
	a, _ := socketPair(t)
	big := make([]byte, control.MaxMsgSize+1)
	for i := range big {
		big[i] = 'x'
	}
	err := control.NewConn(a).SendMsg(control.Msg{V: control.Version, Type: control.TypeError, Error: string(big)}, nil)
	if err == nil {
		t.Fatal("expected oversize message to be rejected")
	}
}

func TestCoalescedMessages(t *testing.T) {
	a, b := socketPair(t)
	ka, kb := control.NewConn(a), control.NewConn(b)

	for i := 0; i < 3; i++ {
		if err := ka.SendMsg(control.Msg{V: control.Version, Type: control.TypeStatus, ID: "s"}, nil); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		m, _, err := kb.RecvMsg()
		if err != nil {
			t.Fatalf("recv %d: %v", i, err)
		}
		if m.Type != control.TypeStatus || m.ID != "s" {
			t.Fatalf("recv %d: got %+v", i, m)
		}
	}
}

func TestRecvMsgDuringConcurrentClose(t *testing.T) {
	for i := 0; i < 100; i++ {
		a, b := socketPair(t)
		ka := control.NewConn(a)

		done := make(chan error, 1)
		go func() {
			for {
				if _, _, err := ka.RecvMsg(); err != nil {
					done <- err
					return
				}
			}
		}()

		_, _ = b.Write([]byte(`{"v":1,"type":"stat`))
		_ = a.Close()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: reader never returned after the connection closed", i)
		}
		_ = b.Close()
	}
}

func TestPeerSurvivesCloseUnderRead(t *testing.T) {
	a, b := socketPair(t)
	p := control.NewPeer(control.NewConn(a), nil)

	done := make(chan error, 1)
	go func() { done <- p.Serve() }()

	_, _ = b.Write([]byte(`{"v":1,"type":"stat`))
	_ = p.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve returned nil after the connection was closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the connection was closed")
	}
}

func TestFDRoundTrip(t *testing.T) {
	a, b := socketPair(t)
	ka, kb := control.NewConn(a), control.NewConn(b)

	payloadFDs, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	mine := adoptUnix(t, payloadFDs[0])
	passed := payloadFDs[1]

	err = ka.SendMsg(control.Msg{
		V: control.Version, Type: control.TypeCopy, ID: "id1", Protocol: control.ProtoTCP,
	}, []int{passed, passed})
	if err != nil {
		t.Fatalf("send with fds: %v", err)
	}
	_ = unix.Close(passed)

	m, fds, err := kb.RecvMsg()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if m.Type != control.TypeCopy || m.ID != "id1" {
		t.Fatalf("unexpected message %+v", m)
	}
	if len(fds) != 2 {
		t.Fatalf("expected 2 fds, got %d", len(fds))
	}
	defer control.CloseFDs(fds)

	if _, err := unix.Write(fds[0], []byte("hello")); err != nil {
		t.Fatalf("write to received fd: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := mine.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q", buf)
	}
}
