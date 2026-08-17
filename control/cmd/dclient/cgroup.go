package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cgroupRoot is the standard unified-hierarchy mount point.
const cgroupRoot = "/sys/fs/cgroup"

// dclientSlice holds one child cgroup per VM, so the whole fleet can be
// accounted for -- and killed -- as a unit.
const dclientSlice = "dclient.slice"

// qemuOverheadMiB is what qemu itself costs on top of the guest's RAM: device
// models, the qcow2 cache, its own heap. Setting memory.max to exactly the
// guest size gets the VM OOM-killed as soon as it touches all of its memory.
const qemuOverheadMiB = 256

// cgroup2Available reports whether the unified hierarchy is mounted.
// cgroup.controllers exists only there, which makes it a reliable marker.
func cgroup2Available() bool {
	_, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers"))
	return err == nil
}

// setupCgroup creates the per-VM cgroup and writes its limits, returning the
// directory to spawn into. An empty path means limits could not be applied --
// which is normal when running unprivileged -- and is the caller's cue to warn
// rather than to fail.
func setupCgroup(v vm) (string, error) {
	if !cgroup2Available() {
		return "", fmt.Errorf("cgroup v2 is not mounted at %s", cgroupRoot)
	}

	slice := filepath.Join(cgroupRoot, dclientSlice)
	if err := os.MkdirAll(slice, 0o755); err != nil {
		return "", err
	}
	// Best effort: on a systemd host the controllers are usually already
	// delegated, and where they are not, the limits below simply will not exist.
	_ = os.WriteFile(filepath.Join(slice, "cgroup.subtree_control"), []byte("+cpu +memory"), 0o644)

	dir := filepath.Join(slice, "vm-"+v.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	limits := map[string]string{
		// A period of 100ms is the kernel default; quota is that times the vCPU
		// count, so --cpus is a hard ceiling and not merely a topology hint.
		"cpu.max":    fmt.Sprintf("%d 100000", v.CPUs*100000),
		"memory.max": fmt.Sprintf("%d", int64(v.MemoryMiB+qemuOverheadMiB)*1024*1024),
		// Swapping a guest is worse than failing to allocate: the guest cannot
		// tell, and the host pays for it in latency.
		"memory.swap.max": "0",
	}
	for name, value := range limits {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
			_ = os.Remove(dir)
			return "", fmt.Errorf("could not set %s: %w", name, err)
		}
	}
	return dir, nil
}

// removeCgroup tears down a VM's cgroup. It only succeeds once the cgroup is
// empty, so it must be called after the process is gone.
func removeCgroup(path string) error {
	if path == "" || !strings.HasPrefix(path, cgroupRoot+"/") {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
