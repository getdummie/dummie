package dpipe

import (
	"bufio"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestAgentTLD(t *testing.T) {
	for host, want := range map[string]string{
		"one.shell.vm.example.com": "vm.example.com",
		"one.shell.local":          "local",
		"shell.local":              "",
	} {
		if got := agentTLD(host); got != want {
			t.Errorf("agentTLD(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestValidTLD(t *testing.T) {
	if !validTLD("vm.example-1.com") {
		t.Error("a plain tld was refused")
	}
	for _, bad := range []string{"a;rm -rf", "a b", "$(x)", "A.com"} {
		if validTLD(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestPumpAgentToWSSpotsOldDinit(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ws := &wsConn{c: a, br: bufio.NewReader(a)}
	go func() { _, _ = b.Read(make([]byte, 1024)) }()
	var idle idleClock
	err := pumpAgentToWS(ws, bufio.NewReader(strings.NewReader("dinit is a guest init: boot it...\n")), &idle)
	if !errors.Is(err, errNotAgent) {
		t.Fatalf("err = %v, want errNotAgent", err)
	}
}
