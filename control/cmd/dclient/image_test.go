package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripContainerMarkers(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, m := range containerMarkers {
		if err := os.WriteFile(filepath.Join(root, m), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := stripContainerMarkers(root); err != nil {
		t.Fatal(err)
	}
	for _, m := range containerMarkers {
		if _, err := os.Lstat(filepath.Join(root, m)); !os.IsNotExist(err) {
			t.Errorf("%s survived; systemd would still ignore the kernel command line", m)
		}
	}
	if err := stripContainerMarkers(root); err != nil {
		t.Errorf("stripping an already-stripped rootfs failed: %v", err)
	}
}

func TestImageRecipeKeysEveryBuild(t *testing.T) {
	if imageRecipe("", "") == "" {
		t.Error("a keyless build has no recipe, so its images can never be invalidated")
	}
	if imageRecipe("ssh-ed25519 AAAA one", "") == imageRecipe("", "") {
		t.Error("the key is not in the recipe, so a rotated key never reaches a guest")
	}
	if imageRecipe("", "10.0.0.1") == imageRecipe("", "10.0.0.2") {
		t.Error("the resolver is not in the recipe, so a moved gateway is served the old image")
	}
}

func TestEnsureResolvConfWritesEmptyFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "etc", "resolv.conf")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureResolvConf(root, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "nameserver 10.0.0.1") {
		t.Errorf("resolv.conf does not point at the resolver:\n%s", b)
	}
}

func TestEnsureResolvConfLeavesSymlinkAlone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "etc", "resolv.conf")
	if err := os.Symlink("../run/systemd/resolve/stub-resolv.conf", p); err != nil {
		t.Fatal(err)
	}
	if err := ensureResolvConf(root, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced, taking resolv.conf away from whatever manages it")
	}
}
