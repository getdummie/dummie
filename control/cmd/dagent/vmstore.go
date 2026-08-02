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

// On-disk layout, all under the data dir:
//
//   images/                     shared cache of downloaded kernels and images
//   vms/<id>/vm.json            desired state, the record of what was asked for
//   vms/<id>/qemu.pid           pid of the running qemu, absent when stopped
//   vms/<id>/qmp.sock           control socket (stop, later: everything else)
//   vms/<id>/console.sock       serial console, what `vm console` attaches to
//   vms/<id>/console.log        every byte the guest ever wrote to the console
//   vms/<id>/qemu.log           qemu's own stderr, the first place to look
//   vms/<id>/root.qcow2         per-VM overlay over the cached base image
const (
  vmConfigFile   = "vm.json"
  vmPIDFile      = "qemu.pid"
  vmQMPSocket    = "qmp.sock"
  vmConsoleSock  = "console.sock"
  vmConsoleLog   = "console.log"
  vmQEMULog      = "qemu.log"
  vmOverlayImage = "root.qcow2"
)

// bootMode picks the machine shape. The two are genuinely different machines,
// not a flag on one: direct boot skips the firmware entirely.
type bootMode string

const (
  // bootDirect is a microVM: no BIOS, no bootloader, kernel handed to qemu.
  bootDirect bootMode = "direct"
  // bootDisk is a conventional machine booting a full disk image via firmware.
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

// vm is the desired state of one machine. It is written once at create and is
// the input a reconciler would diff against later, so it holds what was asked
// for -- not what the kernel currently happens to be doing.
type vm struct {
  ID        string    `json:"id"`
  Name      string    `json:"name"`
  CreatedAt time.Time `json:"created_at"`

  Boot      bootMode `json:"boot"`
  CPUs      int      `json:"cpus"`
  MemoryMiB int      `json:"memory_mib"`

  // Base artifacts in the shared image cache. Kernel/Initrd/Append are direct
  // boot only; Backing is the rootfs (direct) or the whole disk (disk).
  Kernel  string `json:"kernel,omitempty"`
  Initrd  string `json:"initrd,omitempty"`
  Append  string `json:"append,omitempty"`
  Backing string `json:"backing"`

  Firmware string `json:"firmware,omitempty"` // -bios, when the default will not do

  Disk   string `json:"disk"`             // per-VM overlay
  Cgroup string `json:"cgroup,omitempty"` // empty when limits could not be applied
}

// newVMID is short enough to type and wide enough that collisions inside one
// host are not a real concern.
func newVMID() (string, error) {
  var b [3]byte
  if _, err := rand.Read(b[:]); err != nil {
    return "", err
  }
  return hex.EncodeToString(b[:]), nil
}

// --- paths ------------------------------------------------------------------

// defaultDataDir mirrors defaultStateDir: prefer the system location, fall back
// to a user-writable one so the agent is usable unprivileged in development.
func defaultDataDir() string {
  const system = "/var/lib/dagent"
  if writableDir(system) {
    return system
  }
  if dir, err := os.UserCacheDir(); err == nil {
    return filepath.Join(dir, "dagent")
  }
  return ".dagent"
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

func imagesDir(data string) string      { return filepath.Join(data, "images") }
func vmsDir(data string) string         { return filepath.Join(data, "vms") }
func vmDir(data, id string) string      { return filepath.Join(vmsDir(data), id) }
func vmPath(data, id, f string) string  { return filepath.Join(vmDir(data, id), f) }

// --- persistence ------------------------------------------------------------

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
      continue // a half-created directory is not a reason to fail the listing
    }
    vms = append(vms, v)
  }
  sort.Slice(vms, func(i, j int) bool { return vms[i].CreatedAt.Before(vms[j].CreatedAt) })
  return vms, nil
}

// --- liveness ---------------------------------------------------------------

// vmPID returns the running qemu's pid, or 0. The cmdline is checked because a
// stale pid file plus pid reuse would otherwise let us signal an unrelated
// process.
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

// signalVM is a no-op when the VM is not running.
func signalVM(data, id string, sig syscall.Signal) error {
  pid := vmPID(data, id)
  if pid == 0 {
    return nil
  }
  return syscall.Kill(pid, sig)
}
