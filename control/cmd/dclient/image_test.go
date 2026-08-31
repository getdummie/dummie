package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"control/internal/proto"
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
	if imageRecipe("", "", "") == "" {
		t.Error("a keyless build has no recipe, so its images can never be invalidated")
	}
	if imageRecipe("ssh-ed25519 AAAA one", "", "") == imageRecipe("", "", "") {
		t.Error("the key is not in the recipe, so a rotated key never reaches a guest")
	}
	if imageRecipe("", "10.0.0.1", "") == imageRecipe("", "10.0.0.2", "") {
		t.Error("the resolver is not in the recipe, so a moved gateway is served the old image")
	}
	if imageRecipe("", "", "aaaa") == imageRecipe("", "", "bbbb") {
		t.Error("the guest init is not in the recipe, so an upgraded dclient reuses the old rootfs")
	}
}

func TestImageConfigRecipeKeysTheCache(t *testing.T) {
	if imageConfigRecipe(nil) != "" {
		t.Error("an image with no config should not change the cache key of every existing rootfs")
	}
	base := &proto.ImageConfig{User: "appuser", Cmd: []string{"/app"}, Env: []string{"PORT=8080"}}
	if imageConfigRecipe(base) == "" || imageConfigRecipe(base) == imageConfigRecipe(nil) {
		t.Error("a config is not in the recipe, so an edited image is never rebuilt")
	}
	for name, other := range map[string]*proto.ImageConfig{
		"user":       {User: "root", Cmd: base.Cmd, Env: base.Env},
		"cmd":        {User: base.User, Cmd: []string{"/other"}, Env: base.Env},
		"env":        {User: base.User, Cmd: base.Cmd, Env: []string{"PORT=9000"}},
		"entrypoint": {User: base.User, Entrypoint: []string{"sh"}, Cmd: base.Cmd, Env: base.Env},
	} {
		if imageConfigRecipe(base) == imageConfigRecipe(other) {
			t.Errorf("a changed %s does not change the cache key, so the old rootfs is reused", name)
		}
	}
	// The same config has to key the same way every time, or every create
	// rebuilds a rootfs it already had.
	if imageConfigRecipe(base) != imageConfigRecipe(&proto.ImageConfig{
		User: "appuser", Cmd: []string{"/app"}, Env: []string{"PORT=8080"},
	}) {
		t.Error("an identical config keyed differently, so rootfs images are never reused")
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

func TestImageInit(t *testing.T) {
	root := t.TempDir()
	if got := imageInit(root); got != "" {
		t.Errorf("an empty rootfs claims to have %q as its init", got)
	}

	if err := os.MkdirAll(filepath.Join(root, "lib", "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "systemd", "systemd"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := imageInit(root); got != "/lib/systemd/systemd" {
		t.Errorf("imageInit = %q, want /lib/systemd/systemd", got)
	}
}

func TestImageInitIgnoresANonExecutableInit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sbin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sbin", "init"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := imageInit(root); got != "" {
		t.Errorf("a non-executable /sbin/init was accepted as %q", got)
	}
}
