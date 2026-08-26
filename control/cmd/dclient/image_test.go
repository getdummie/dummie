package main

import (
	"os"
	"path/filepath"
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
	// A rootfs that never had them is the normal case for an image not built from
	// a container, so a second pass has to be a no-op rather than an error.
	if err := stripContainerMarkers(root); err != nil {
		t.Errorf("stripping an already-stripped rootfs failed: %v", err)
	}
}

// A host with no dpipe key still gets a keyed cache entry: what the build does
// to the rootfs is part of the image even when no key goes in, so an image built
// by an older dclient must not be served to a create expecting this one's.
func TestImageRecipeKeysEveryBuild(t *testing.T) {
	if imageRecipe("") == "" {
		t.Error("a keyless build has no recipe, so its images can never be invalidated")
	}
	if imageRecipe("ssh-ed25519 AAAA one") == imageRecipe("") {
		t.Error("the key is not in the recipe, so a rotated key never reaches a guest")
	}
}
