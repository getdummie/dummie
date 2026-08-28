package main

import (
	"strings"
	"testing"
)

func TestGuestHostname(t *testing.T) {
	ok := []string{"a", "hello-kitty", "abc123", "6f1a2b"}
	for _, name := range ok {
		if got := guestHostname(name); got != name {
			t.Errorf("guestHostname(%q) = %q, want it kept as-is", name, got)
		}
	}
	bad := []string{"", "-lead", "trail-", "has space", "dots.in.name", "under_score",
		strings.Repeat("a", 64)}
	for _, name := range bad {
		if got := guestHostname(name); got != "" {
			t.Errorf("guestHostname(%q) = %q, want \"\"", name, got)
		}
	}
}

func TestWithHostname(t *testing.T) {
	base := "console=ttyS0 root=/dev/vda rw"

	if got, want := withHostname(base, "kitty"), base+" systemd.hostname=kitty"; got != want {
		t.Errorf("withHostname = %q, want %q", got, want)
	}
	own := base + " systemd.hostname=mine"
	if got := withHostname(own, "kitty"); got != own {
		t.Errorf("withHostname overrode an explicit systemd.hostname=: %q", got)
	}
	if got := withHostname(base, "bad name"); got != base {
		t.Errorf("withHostname added an unusable name: %q", got)
	}
}
