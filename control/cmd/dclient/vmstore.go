package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	vmConfigFile   = "vm.json"
	vmPIDFile      = "qemu.pid"
	vmQEMULog      = "qemu.log"
	vmOverlayImage = "root.qcow2"

	vmRunDir      = "run"
	vmQMPSocket   = vmRunDir + "/qmp.sock"
	vmConsoleSock = vmRunDir + "/console.sock"
	vmConsoleLog  = vmRunDir + "/console.log"
)

type bootMode string

const (
	bootDirect bootMode = "direct"
	bootDisk bootMode = "disk"
)

func parseBootMode(s string) (bootMode, error) {
	switch bootMode(s) {
	case bootDirect:
		return bootDirect, nil
	case bootDisk:
		return bootDisk, nil
	default:
		return "", fmt.Errorf("--boot must be %q or %q, got %q", bootDirect, bootDisk, s)
	}
}

type vm struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`

	Boot      bootMode `json:"boot"`
	CPUs      int      `json:"cpus"`
	MemoryMiB int      `json:"memory_mib"`

	Kernel  string `json:"kernel,omitempty"`
	Initrd  string `json:"initrd,omitempty"`
	Append  string `json:"append,omitempty"`
	Backing string `json:"backing"`

	Firmware string `json:"firmware,omitempty"`

	Net *vmNet `json:"net,omitempty"`

	Disk   string `json:"disk"`
	Cgroup string `json:"cgroup,omitempty"`

	UID int `json:"uid,omitempty"`
}

func newVMID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func defaultDataDir() string {
	const system = "/var/lib/dclient"
	if writableDir(system) {
		return system
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "dclient")
	}
	return ".dclient"
}

func writableDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".writable-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func imagesDir(data string) string     { return filepath.Join(data, "images") }
func vmsDir(data string) string        { return filepath.Join(data, "vms") }
func vmDir(data, id string) string     { return filepath.Join(vmsDir(data), id) }
func vmPath(data, id, f string) string { return filepath.Join(vmDir(data, id), f) }

func saveVM(data string, v vm) error {
	dir := vmDir(data, v.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, vmConfigFile), b, 0o600)
}

func loadVM(data, id string) (vm, error) {
	var v vm
	b, err := os.ReadFile(vmPath(data, id, vmConfigFile))
	if err != nil {
		return v, fmt.Errorf("no such vm %q: %w", id, err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("vm %s has a corrupt %s: %w", id, vmConfigFile, err)
	}
	return v, nil
}

func listVMs(data string) ([]vm, error) {
	entries, err := os.ReadDir(vmsDir(data))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var vms []vm
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v, err := loadVM(data, e.Name())
		if err != nil {
			continue
		}
		vms = append(vms, v)
	}
	sort.Slice(vms, func(i, j int) bool { return vms[i].CreatedAt.Before(vms[j].CreatedAt) })
	return vms, nil
}

func vmPID(data, id string) int {
	b, err := os.ReadFile(vmPath(data, id, vmPIDFile))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return 0
	}
	if !strings.Contains(string(cmdline), vmDir(data, id)) {
		return 0
	}
	return pid
}

func writePID(data, id string, pid int) error {
	return os.WriteFile(vmPath(data, id, vmPIDFile), []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func signalVM(data, id string, sig syscall.Signal) error {
	pid := vmPID(data, id)
	if pid == 0 {
		return nil
	}
	return syscall.Kill(pid, sig)
}
