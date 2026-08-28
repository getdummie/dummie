package dpipe

import (
	"testing"
	"time"
)

func newConsoleServer(t *testing.T, maxPerHost int) *Server {
	t.Helper()
	return &Server{
		cfg:      &Config{Console: ConsoleConfig{Enabled: true, MaxSessionsPerHost: maxPerHost}},
		log:      testLogger(),
		consoles: map[string]int{},
	}
}

func TestConsoleSessionCapIsPerHost(t *testing.T) {
	s := newConsoleServer(t, 2)

	if !s.acquireConsole("one.vm.local") || !s.acquireConsole("one.vm.local") {
		t.Fatal("the first two sessions for a host must be admitted")
	}
	if s.acquireConsole("one.vm.local") {
		t.Fatal("a third session for the same host must be refused")
	}
	if !s.acquireConsole("two.vm.local") {
		t.Fatal("a different host must still be admitted")
	}

	s.releaseConsole("one.vm.local")
	if !s.acquireConsole("one.vm.local") {
		t.Fatal("a released slot must be reusable")
	}
}

func TestConsoleReleaseForgetsIdleHosts(t *testing.T) {
	s := newConsoleServer(t, 1)
	if !s.acquireConsole("one.vm.local") {
		t.Fatal("first session refused")
	}
	s.releaseConsole("one.vm.local")
	if len(s.consoles) != 0 {
		t.Fatalf("counters left behind: %v", s.consoles)
	}
}

func TestConsoleDefaultLimits(t *testing.T) {
	var c ConsoleConfig
	if c.maxPerHost() != defaultConsoleMaxPerHost {
		t.Errorf("maxPerHost = %d", c.maxPerHost())
	}
	if c.idleTimeout() != defaultConsoleIdleTimeout {
		t.Errorf("idleTimeout = %s", c.idleTimeout())
	}
}

func TestValidGeometry(t *testing.T) {
	cases := []struct {
		cols, rows int
		want       bool
	}{
		{80, 24, true},
		{1, 1, true},
		{0, 24, false},
		{80, 0, false},
		{-1, 24, false},
		{100000, 24, false},
	}
	for _, c := range cases {
		if got := validGeometry(c.cols, c.rows); got != c.want {
			t.Errorf("validGeometry(%d, %d) = %v", c.cols, c.rows, got)
		}
	}
}

func TestIdleClockFiresOnlyWhenIdle(t *testing.T) {
	var idle idleClock
	idle.touch()

	stop := make(chan struct{})
	fired := make(chan bool, 1)
	go func() { fired <- idle.watch(200*time.Millisecond, stop) }()

	for i := 0; i < 6; i++ {
		time.Sleep(50 * time.Millisecond)
		idle.touch()
	}
	select {
	case v := <-fired:
		t.Fatalf("watch returned %v while the session was active", v)
	default:
	}

	select {
	case v := <-fired:
		if !v {
			t.Fatal("watch reported no timeout")
		}
	case <-time.After(2 * time.Second):
		close(stop)
		t.Fatal("watch did not fire after the idle window")
	}
}

func TestIdleClockStops(t *testing.T) {
	var idle idleClock
	idle.touch()

	stop := make(chan struct{})
	fired := make(chan bool, 1)
	go func() { fired <- idle.watch(10*time.Second, stop) }()
	close(stop)

	select {
	case v := <-fired:
		if v {
			t.Fatal("watch reported a timeout after stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not return after stop")
	}
}
