package main

import (
  "bufio"
  "context"
  "fmt"
  "os"
  "os/exec"
  "path/filepath"
  "runtime"
  "slices"
  "strings"
  "syscall"

  "github.com/google/nftables"
  "github.com/urfave/cli/v3"
)

// result is a check's verdict. warn is reported but does not fail the run.
type result int

const (
  pass result = iota
  warn
  fail
)

func (r result) String() string {
  switch r {
  case pass:
    return "PASS"
  case warn:
    return "WARN"
  default:
    return "FAIL"
  }
}

// check is one preflight test. Add to `checks` to grow the suite -- nothing
// else needs to change.
type check struct {
  name string
  run  func() (result, string)
}

var checks = []check{
  {"os is linux", checkLinux},
  {"distribution is ubuntu or debian", checkDistro},
  {"qemu is installed", checkQEMU},
  {"qemu-img is installed", checkQEMUImg},
  {"hardware virtualisation is usable", checkKVM},
  {"cgroup v2 is available", checkCgroup2},
  {"vms can run as their own uid", checkVMUID},
  {"rootfs images can be built from tars", checkRootfsTools},
  {"tap devices can be created", checkTun},
  {"nftables is usable", checkNftables},
  {"ip forwarding is enabled", checkForwarding},
  {"an uplink for egress exists", checkUplink},
  {"nfqueue matches the suricata configuration", checkQueues},
  {"the suricata container is running", checkSuricata},
  {"vector is shipping suricata events", checkVector},
  {"docker is not dropping vm traffic", checkDockerCompat},
  {"data directory is writable", checkDataDir},
}

// supportedDistros are the os-release IDs the agent is tested against.
var supportedDistros = []string{"ubuntu", "debian"}

func doctorCommand() *cli.Command {
  return &cli.Command{
    Name:  "doctor",
    Usage: "check that this machine can run the agent",
    Action: func(ctx context.Context, cmd *cli.Command) error {
      return runDoctor()
    },
  }
}

func runDoctor() error {
  width := 0
  for _, c := range checks {
    if len(c.name) > width {
      width = len(c.name)
    }
  }

  failed := 0
  for _, c := range checks {
    res, detail := c.run()
    if res == fail {
      failed++
    }
    fmt.Printf("%-4s  %-*s  %s\n", res, width, c.name, detail)
  }

  if failed > 0 {
    return cli.Exit(fmt.Sprintf("%d check(s) failed", failed), 1)
  }
  return nil
}

func checkLinux() (result, string) {
  if runtime.GOOS != "linux" {
    return fail, fmt.Sprintf("this is %s; the agent only supports linux", runtime.GOOS)
  }
  return pass, "linux/" + runtime.GOARCH
}

func checkDistro() (result, string) {
  rel, err := readOSRelease()
  if err != nil {
    return fail, "could not read /etc/os-release: " + err.Error()
  }

  if slices.Contains(supportedDistros, rel["ID"]) {
    return pass, describeOSRelease(rel)
  }
  // Derivatives (Mint, Pop!_OS, Raspberry Pi OS...) will mostly behave, but
  // they are not what we test against.
  for _, id := range supportedDistros {
    if slices.Contains(strings.Fields(rel["ID_LIKE"]), id) {
      return warn, describeOSRelease(rel) + " (" + id + "-derived, not " + id + ")"
    }
  }
  return fail, describeOSRelease(rel) + " is not ubuntu or debian"
}

func checkQEMU() (result, string) {
  bin := qemuBinary()
  path, err := exec.LookPath(bin)
  if err != nil {
    return fail, bin + " is not on PATH; install qemu-system-x86 (or the arch equivalent)"
  }
  out, err := exec.Command(bin, "--version").Output()
  if err != nil {
    return fail, path + " will not run: " + err.Error()
  }
  version, _, _ := strings.Cut(string(out), "\n")
  return pass, strings.TrimSpace(version) + " at " + path
}

func checkQEMUImg() (result, string) {
  path, err := exec.LookPath("qemu-img")
  if err != nil {
    return fail, "qemu-img is not on PATH; install qemu-utils (per-vm disk overlays need it)"
  }
  return pass, path
}

// checkKVM is a warning, not a failure: qemu still runs guests under TCG
// emulation, just an order of magnitude slower.
func checkKVM() (result, string) {
  f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
  if err != nil {
    if os.IsNotExist(err) {
      return warn, "/dev/kvm is missing; vms will fall back to slow software emulation"
    }
    return warn, "/dev/kvm is not writable by this user (" + err.Error() +
      "); add the user to the kvm group, or run as root"
  }
  _ = f.Close()
  return pass, "/dev/kvm is readable and writable"
}

// checkCgroup2 is a warning for the same reason: without it the vm boots, it
// just boots without cpu and memory ceilings.
func checkCgroup2() (result, string) {
  if !cgroup2Available() {
    return warn, "no unified hierarchy at " + cgroupRoot + "; vms will run without cpu or memory limits"
  }
  // Creating the slice is the part that actually needs privilege, so it is the
  // honest thing to test.
  if err := os.MkdirAll(filepath.Join(cgroupRoot, dagentSlice), 0o755); err != nil {
    return warn, "cannot create " + dagentSlice + " (" + err.Error() + "); vms will run without limits"
  }
  return pass, cgroupRoot + ", " + dagentSlice + " is writable"
}

// checkVMUID covers the two things a per-VM uid needs from the host: privilege
// to drop, and a /dev/kvm the dropped-to uid can still reach. Both are warnings
// -- the VM boots either way, less isolated or slower.
func checkVMUID() (result, string) {
  if os.Geteuid() != 0 {
    return warn, "not root; vms will run as this user and share their files with each other"
  }
  groups, kvm := kvmAccess()
  reach := fmt.Sprintf("uids %d-%d", uidBase, uidBase+uidCount-1)
  switch {
  case !kvm:
    return warn, reach + ", but " + describeKVMDevice() +
      ": no unprivileged uid can open it, so vms will fall back to slow software emulation"
  case len(groups) == 0:
    return pass, reach + ", /dev/kvm is world-writable"
  default:
    return pass, fmt.Sprintf("%s, /dev/kvm via group %d", reach, groups[0])
  }
}

// describeKVMDevice names the device's owner and mode rather than the reason it
// is unreachable. "Not reachable by an unprivileged uid" has several causes --
// no group access at all, a root-owned group, a hand-made device node -- and the
// numbers say which one it is without a second round of questions.
func describeKVMDevice() string {
  info, err := os.Stat("/dev/kvm")
  if err != nil {
    return "/dev/kvm cannot be read (" + err.Error() + ")"
  }
  st, ok := info.Sys().(*syscall.Stat_t)
  if !ok {
    return "/dev/kvm"
  }
  return fmt.Sprintf("/dev/kvm is owned %d:%d mode %04o", st.Uid, st.Gid, info.Mode().Perm())
}

// checkRootfsTools covers --rootfs-tar only, so a missing tool is a warning:
// everything else about the agent still works without it.
func checkRootfsTools() (result, string) {
  var missing []string
  for _, bin := range []string{"tar", "mkfs.ext4"} {
    if _, err := exec.LookPath(bin); err != nil {
      missing = append(missing, bin)
    }
  }
  if len(missing) > 0 {
    return warn, strings.Join(missing, " and ") + " not on PATH; --rootfs-tar will not work (install tar and e2fsprogs)"
  }
  return pass, "tar and mkfs.ext4 are present"
}

// checkTun is a hard failure: without /dev/net/tun no VM can have a network
// device, and the fd handoff that keeps QEMU unprivileged depends on it.
func checkTun() (result, string) {
  f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
  if err != nil {
    if os.IsNotExist(err) {
      return fail, "/dev/net/tun is missing; load the tun module"
    }
    return warn, "/dev/net/tun is not writable by this user (" + err.Error() + "); networking needs root"
  }
  _ = f.Close()
  return pass, "/dev/net/tun is readable and writable"
}

// checkNftables tries the same netlink socket the policy layer uses. Listing is
// harmless and proves both the kernel support and our permission to use it.
func checkNftables() (result, string) {
  c, err := nftables.New()
  if err != nil {
    return fail, "could not open a netlink socket: " + err.Error()
  }
  tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
  if err != nil {
    return warn, "cannot list nftables (" + err.Error() + "); policy needs root"
  }
  for _, t := range tables {
    if t.Name == nftTable {
      return pass, "the " + nftTable + " table is installed"
    }
  }
  return pass, fmt.Sprintf("%d inet table(s); %s will be installed on first use", len(tables), nftTable)
}

func checkForwarding() (result, string) {
  b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
  if err != nil {
    return fail, err.Error()
  }
  if len(b) > 0 && b[0] == '1' {
    return pass, "net.ipv4.ip_forward=1"
  }
  // Turned on automatically when a VM is created, so this is only a warning --
  // but it will not survive a reboot unless it is also set in sysctl.d.
  return warn, "net.ipv4.ip_forward=0; dagent will enable it, but set it in /etc/sysctl.d to make it stick"
}

func checkUplink() (result, string) {
  name, err := defaultUplink()
  if err != nil {
    return warn, err.Error() + "; vm egress cannot be masqueraded"
  }
  return pass, name
}

func checkSuricata() (result, string) {
  cfg, err := loadNetConfig(defaultDataDir())
  if err != nil {
    return warn, "could not read the network config: " + err.Error()
  }
  return suricataStatus(cfg)
}

// checkQueues compares the queues something is actually bound to against the
// number dagent hands packets to. A mismatch is not cosmetic: traffic hashed to
// an unbound queue is dropped, so it takes VM egress down for a fraction of
// flows in a way that looks like packet loss rather than policy.
func checkQueues() (result, string) {
  cfg, err := loadNetConfig(defaultDataDir())
  if err != nil {
    return warn, "could not read the network config: " + err.Error()
  }
  if !cfg.Suricata {
    return pass, "suricata mode is off; nothing is queued"
  }

  const p = "/proc/net/netfilter/nfnetlink_queue"
  b, err := os.ReadFile(p)
  if err != nil {
    if os.IsNotExist(err) {
      return fail, "suricata mode is on but " + p + " does not exist; the kernel has no NFQUEUE support"
    }
    return warn, "cannot read " + p + " (" + err.Error() + "); run as root"
  }

  // One row per bound queue.
  bound := 0
  if rows := strings.TrimSpace(string(b)); rows != "" {
    bound = len(strings.Split(rows, "\n"))
  }

  switch {
  case bound == 0:
    return fail, "suricata mode is on but nothing is bound to any queue; all vm egress is being dropped"
  case bound != int(cfg.Queues):
    return fail, fmt.Sprintf("dagent queues to %d queues but %d are bound; start suricata with %d -q flags",
      cfg.Queues, bound, cfg.Queues)
  }
  return pass, fmt.Sprintf("%d queues bound, matching net.json", bound)
}

func checkDockerCompat() (result, string) {
  needed, reason := dockerCompatNeeded()
  if needed {
    return warn, reason
  }
  return pass, reason
}

func checkDataDir() (result, string) {
  dir := defaultDataDir()
  if !writableDir(dir) {
    return fail, "cannot write to " + dir
  }
  if dir != "/var/lib/dagent" {
    return warn, dir + " (not /var/lib/dagent; vms will not be found by a root-run agent)"
  }
  return pass, dir
}

func describeOSRelease(rel map[string]string) string {
  name := rel["PRETTY_NAME"]
  if name == "" {
    name = rel["NAME"] + " " + rel["VERSION_ID"]
  }
  if strings.TrimSpace(name) == "" {
    return "unknown distribution"
  }
  return strings.TrimSpace(name)
}

// osReleasePath is a variable so tests can point it elsewhere.
var osReleasePath = "/etc/os-release"

// readOSRelease parses the KEY=value format of os-release(5), stripping the
// optional quoting.
func readOSRelease() (map[string]string, error) {
  f, err := os.Open(osReleasePath)
  if err != nil {
    return nil, err
  }
  defer f.Close()

  rel := map[string]string{}
  sc := bufio.NewScanner(f)
  for sc.Scan() {
    line := strings.TrimSpace(sc.Text())
    if line == "" || strings.HasPrefix(line, "#") {
      continue
    }
    k, v, ok := strings.Cut(line, "=")
    if !ok {
      continue
    }
    v = strings.TrimSpace(v)
    if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
      v = v[1 : len(v)-1]
    }
    rel[strings.TrimSpace(k)] = v
  }
  return rel, sc.Err()
}

// osFacts is what connect reports at enrollment: the distribution id and its
// version, falling back to runtime info when os-release is unavailable.
func osFacts() (osName, osVersion string) {
  osName = runtime.GOOS
  rel, err := readOSRelease()
  if err != nil {
    return osName, ""
  }
  if id := rel["ID"]; id != "" {
    osName = id
  }
  return osName, rel["VERSION_ID"]
}
