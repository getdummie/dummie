package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
