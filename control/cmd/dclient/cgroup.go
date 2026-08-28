package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const cgroupRoot = "/sys/fs/cgroup"

const dclientSlice = "dclient.slice"

const qemuOverheadMiB = 256

func cgroup2Available() bool {
	_, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers"))
	return err == nil
}

func setupCgroup(v vm) (string, error) {
	if !cgroup2Available() {
		return "", fmt.Errorf("cgroup v2 is not mounted at %s", cgroupRoot)
	}

	slice := filepath.Join(cgroupRoot, dclientSlice)
	if err := os.MkdirAll(slice, 0o755); err != nil {
		return "", err
	}
	_ = os.WriteFile(filepath.Join(slice, "cgroup.subtree_control"), []byte("+cpu +memory"), 0o644)

	dir := filepath.Join(slice, "vm-"+v.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	limits := map[string]string{
		"cpu.max":    fmt.Sprintf("%d 100000", v.CPUs*100000),
		"memory.max": fmt.Sprintf("%d", int64(v.MemoryMiB+qemuOverheadMiB)*1024*1024),
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

func removeCgroup(path string) error {
	if path == "" || !strings.HasPrefix(path, cgroupRoot+"/") {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
