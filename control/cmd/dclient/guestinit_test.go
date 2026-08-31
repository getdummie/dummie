package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"control/internal/proto"
)

func TestInjectImageConfig(t *testing.T) {
	root := t.TempDir()
	cfg := &proto.ImageConfig{
		User: "appuser",
		Cmd:  []string{"sh", "-c", "exec marimo edit -p $PORT"},
		Env:  []string{"PORT=8080", "HOST=0.0.0.0"},
	}
	if err := injectImageConfig(root, cfg); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(root, strings.TrimPrefix(guestImageConfigPath, "/"))
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("the config was not written into the rootfs: %v", err)
	}
	// The environment can carry build-time tokens, so it must not be readable
	// by the guest's own unprivileged users.
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s is mode %o, want 600", guestImageConfigPath, perm)
	}

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got proto.ImageConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("dinit could not parse what dclient wrote: %v", err)
	}
	if got.User != cfg.User || strings.Join(got.Cmd, "\x00") != strings.Join(cfg.Cmd, "\x00") {
		t.Errorf("round-tripped to %+v, want %+v", got, cfg)
	}
}

func TestInjectImageConfigWithNothingToRecord(t *testing.T) {
	root := t.TempDir()
	if err := injectImageConfig(root, nil); err != nil {
		t.Fatal(err)
	}
	// An image with no config must leave the rootfs exactly as it was, so a
	// guest on an older dinit is unaffected.
	if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(guestImageConfigPath, "/"))); !os.IsNotExist(err) {
		t.Errorf("a nil config still wrote %s", guestImageConfigPath)
	}
}

func TestWithGuestInit(t *testing.T) {
	base := "console=ttyS0 root=/dev/vda rw"
	v := vm{GuestInit: true, Net: &vmNet{IP: "10.64.0.7", Gateway: "10.64.0.1", DNS: "10.64.0.2"}}

	line := withGuestInit(base, v)
	for _, want := range []string{"init=" + guestInitPath, "dclient.ip=10.64.0.7", "dclient.gw=10.64.0.1", "dclient.dns=10.64.0.2"} {
		if !strings.Contains(line, want) {
			t.Errorf("cmdline %q is missing %q", line, want)
		}
	}

	if got := withGuestInit(base, vm{GuestInit: false}); got != base {
		t.Errorf("an image without dinit got %q", got)
	}

	// An operator who passed their own init= keeps it: booting dinit instead
	// would silently ignore what they asked for.
	own := base + " init=/lib/systemd/systemd"
	if got := withGuestInit(own, v); got != own {
		t.Errorf("an explicit init= was overridden: %q", got)
	}

	noNet := withGuestInit(base, vm{GuestInit: true})
	if strings.Contains(noNet, "dclient.ip") {
		t.Errorf("a vm with no network got an address: %q", noNet)
	}
}

func TestCheckGuestInitRejectsNonELF(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dinit")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho not an elf\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkGuestInit(p); err == nil {
		t.Error("a shell script passed as a guest init")
	}
}

// The test binary is built by `go test`, which leaves cgo on by default, so it is
// usually the dynamically linked case this guards against.
func TestCheckGuestInitOnThisBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	err = checkGuestInit(self)
	if err != nil && !strings.Contains(err.Error(), "dynamically linked") {
		t.Errorf("checkGuestInit on the test binary: %v", err)
	}
}
