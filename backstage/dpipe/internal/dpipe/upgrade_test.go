package dpipe

import (
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

func TestSelfUpgrade(t *testing.T) {
	cfg := baseConfig(t)
	old := startServer(t, cfg)
	oldPeer := dialControl(t, cfg.ControlSocket, nil)

	echo := echoServer(t)
	clientNear := startCopy(t, oldPeer, echo)

	newSrv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := newSrv.AdoptRunning(); err != nil {
		t.Fatalf("AdoptRunning: %v", err)
	}
	t.Cleanup(newSrv.stopAccepting)

	waitFor(t, "old instance to start draining", func() bool { return old.reg.Draining() })

	if _, err := clientNear.Write([]byte("still here")); err != nil {
		t.Fatalf("write on pre-upgrade conn: %v", err)
	}
	buf := make([]byte, len("still here"))
	_ = clientNear.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(clientNear, buf); err != nil {
		t.Fatalf("read on pre-upgrade conn: %v", err)
	}
	if string(buf) != "still here" {
		t.Fatalf("got %q", buf)
	}
	if old.reg.Active() != 1 {
		t.Fatalf("old active = %d, want 1", old.reg.Active())
	}

	for _, p := range []string{cfg.ControlSocket, cfg.UpgradeSocket} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("socket %s missing after handover: %v", p, err)
		}
	}

	newPeer := dialControl(t, cfg.ControlSocket, nil)
	newClient := startCopy(t, newPeer, echo)
	if _, err := newClient.Write([]byte("new")); err != nil {
		t.Fatalf("write on post-upgrade conn: %v", err)
	}
	nbuf := make([]byte, 3)
	_ = newClient.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(newClient, nbuf); err != nil {
		t.Fatalf("read on post-upgrade conn: %v", err)
	}
	if newSrv.reg.Active() != 1 {
		t.Fatalf("new active = %d, want 1", newSrv.reg.Active())
	}

	_ = clientNear.Close()
	select {
	case <-old.Exit():
	case <-time.After(10 * time.Second):
		t.Fatal("old instance did not exit after draining")
	}
}

func startCopy(t *testing.T, p *control.Peer, target string) net.Conn {
	t.Helper()
	clientNear, clientFar := connPair(t)
	backend, err := net.Dial("tcp", target)
	if err != nil {
		t.Fatalf("dial target: %v", err)
	}
	ctx, cancel := ctx5(t)
	defer cancel()
	err = xnet.WithFD2(clientFar, backend, func(cfd, bfd int) error {
		rep, err := p.Request(ctx, control.Msg{
			V: control.Version, Type: control.TypeCopy, ID: control.NewID(), Protocol: control.ProtoTCP,
		}, []int{cfd, bfd})
		if err != nil {
			return err
		}
		if rep.Type != control.TypeOK {
			return fmt.Errorf("reply %+v", rep)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("copy handoff: %v", err)
	}
	_ = clientFar.Close()
	_ = backend.Close()
	return clientNear
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
