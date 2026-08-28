package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/nftables"
	"github.com/urfave/cli/v3"
)

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

type check struct {
	name string
	run  func() (result, string)
}

var checks = []check{
	{"os is linux", checkLinux},
	{"distribution is supported", checkDistro},
	{"qemu is installed", checkQEMU},
	{"qemu-img is installed", checkQEMUImg},
	{"hardware virtualisation is usable", checkKVM},
	{"cgroup v2 is available", checkCgroup2},
	{"vms can run as their own uid", checkVMUID},
	{"rootfs images can be built from tars", checkRootfsTools},
	{"port 22 is the proxy's", checkSSHPort},
	{"tap devices can be created", checkTun},
	{"nftables is usable", checkNftables},
	{"ip forwarding is enabled", checkForwarding},
	{"an uplink for egress exists", checkUplink},
	{"nfqueue matches the suricata configuration", checkQueues},
	{"the suricata container is running", checkSuricata},
	{"the coredns container is running", checkCoreDNS},
	{"vector is shipping suricata events", checkVector},
	{"docker is not dropping vm traffic", checkDockerCompat},
	{"data directory is writable", checkDataDir},
}

var supportedDistros = []string{"ubuntu", "debian", "arch"}

func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "check that this machine can run the client",
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
		return fail, fmt.Sprintf("this is %s; the client only supports linux", runtime.GOOS)
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
	for _, id := range supportedDistros {
		if slices.Contains(strings.Fields(rel["ID_LIKE"]), id) {
			return warn, describeOSRelease(rel) + " (" + id + "-derived, not " + id + ")"
		}
	}
	return fail, describeOSRelease(rel) + " is not " + strings.Join(supportedDistros, ", ")
}

func checkQEMU() (result, string) {
	bin := qemuBinary()
	path, err := exec.LookPath(bin)
	if err != nil {
		return fail, bin + " is not on PATH; install qemu-system-x86 on debian, qemu-base on arch"
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
		return fail, "qemu-img is not on PATH; install qemu-utils on debian, qemu-img on arch (per-vm disk overlays need it)"
	}
	return pass, path
}

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

func checkCgroup2() (result, string) {
	if !cgroup2Available() {
		return warn, "no unified hierarchy at " + cgroupRoot + "; vms will run without cpu or memory limits"
	}
	if err := os.MkdirAll(filepath.Join(cgroupRoot, dclientSlice), 0o755); err != nil {
		return warn, "cannot create " + dclientSlice + " (" + err.Error() + "); vms will run without limits"
	}
	return pass, cgroupRoot + ", " + dclientSlice + " is writable"
}

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

func checkSSHPort() (result, string) {
	switch owner, held, err := listenerOn(22); {
	case err != nil:
		return warn, "cannot tell what is listening on :22 (" + err.Error() + "); make sure sshd is not"
	case held && owner.pid == 0:
		return warn, "something is listening on :22 but its owner is not visible to this user; re-run as root"
	case held && (owner.name == proxyService || owner.name == dpipeService):
		return pass, fmt.Sprintf("%s holds :22 (pid %d)", owner.name, owner.pid)
	case held && strings.HasPrefix(owner.name, "sshd"):
		return fail, fmt.Sprintf("sshd is listening on :22 (pid %d)"+
			"; move it to another port and restart it -- proxy needs :22 for vm ssh", owner.pid)
	case held:
		return fail, fmt.Sprintf("%s (pid %d) is listening on :22, which proxy needs for vm ssh",
			owner.name, owner.pid)
	}

	bin, err := lookSSHD()
	if err != nil {
		return pass, "nothing on :22 and no sshd on this host; proxy can have it"
	}

	ports, source, err := sshdPorts(bin)
	if err != nil {
		return warn, "cannot read the sshd configuration (" + err.Error() + "); make sure it does not listen on 22"
	}
	if len(ports) == 0 {
		ports = []string{"22"}
		source += " (no Port directive; sshd defaults to 22)"
	}
	if slices.Contains(ports, "22") {
		return fail, "nothing holds :22 yet, but sshd is configured for it per " + source +
			"; move it to another port -- whichever of sshd and proxy starts second will not start"
	}
	return pass, ":22 is free; sshd on " + strings.Join(ports, ", ") + " per " + source
}

var sshdConfigPath = "/etc/ssh/sshd_config"

func lookSSHD() (string, error) {
	if path, err := exec.LookPath("sshd"); err == nil {
		return path, nil
	}
	for _, p := range []string{"/usr/sbin/sshd", "/sbin/sshd"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

func sshdPorts(bin string) ([]string, string, error) {
	if out, err := exec.Command(bin, "-T").Output(); err == nil {
		var ports []string
		for _, line := range strings.Split(string(out), "\n") {
			if p, ok := strings.CutPrefix(strings.TrimSpace(line), "port "); ok {
				ports = append(ports, strings.TrimSpace(p))
			}
		}
		return ports, bin + " -T", nil
	}
	ports, err := parseSSHDPorts(sshdConfigPath, 0)
	return ports, sshdConfigPath, err
}

func parseSSHDPorts(path string, depth int) ([]string, error) {
	if depth > 8 {
		return nil, fmt.Errorf("include nesting too deep at %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var ports []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.ReplaceAll(line, "=", " "), " ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "port":
			ports = append(ports, strings.Fields(value)...)
		case "listenaddress":
			fields := strings.Fields(value)
			if len(fields) == 0 {
				continue
			}
			if _, port, err := net.SplitHostPort(fields[0]); err == nil && port != "" {
				ports = append(ports, port)
			}
		case "include":
			for _, pattern := range strings.Fields(value) {
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(filepath.Dir(path), pattern)
				}
				matches, err := filepath.Glob(pattern)
				if err != nil {
					return nil, err
				}
				for _, m := range matches {
					included, err := parseSSHDPorts(m, depth+1)
					if err != nil {
						return nil, err
					}
					ports = append(ports, included...)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return ports, nil
}

type listener struct {
	pid  int
	name string
}

var procRoot = "/proc"

func listenerOn(port int) (listener, bool, error) {
	var inodes []string
	var readable bool
	var lastErr error
	for _, name := range []string{"net/tcp", "net/tcp6"} {
		found, err := listeningInodes(filepath.Join(procRoot, name), port)
		if err != nil {
			lastErr = err
			continue
		}
		readable = true
		inodes = append(inodes, found...)
	}
	switch {
	case !readable:
		return listener{}, false, lastErr
	case len(inodes) == 0:
		return listener{}, false, nil
	}
	pid, name := socketOwner(inodes)
	return listener{pid: pid, name: name}, true, nil
}

func listeningInodes(path string, port int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const stListen = "0A"
	var inodes []string
	sc := bufio.NewScanner(f)
	sc.Scan()
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || fields[3] != stListen {
			continue
		}
		_, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		bound, err := strconv.ParseUint(hexPort, 16, 32)
		if err != nil || int(bound) != port {
			continue
		}
		inodes = append(inodes, fields[9])
	}
	return inodes, sc.Err()
}

func socketOwner(inodes []string) (int, string) {
	want := make(map[string]bool, len(inodes))
	for _, ino := range inodes {
		want["socket:["+ino+"]"] = true
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, ""
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join(procRoot, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !want[target] {
				continue
			}
			comm, _ := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm"))
			return pid, strings.TrimSpace(string(comm))
		}
	}
	return 0, ""
}

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
	return warn, "net.ipv4.ip_forward=0; dclient will enable it, but set it in /etc/sysctl.d to make it stick"
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

func checkCoreDNS() (result, string) {
	cfg, err := loadNetConfig(defaultDataDir())
	if err != nil {
		return warn, "could not read the network config: " + err.Error()
	}
	return corednsStatus(cfg)
}

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

	bound := 0
	if rows := strings.TrimSpace(string(b)); rows != "" {
		bound = len(strings.Split(rows, "\n"))
	}

	switch {
	case bound == 0:
		return fail, "suricata mode is on but nothing is bound to any queue; all vm egress is being dropped"
	case bound != int(cfg.Queues):
		return fail, fmt.Sprintf("dclient queues to %d queues but %d are bound; start suricata with %d -q flags",
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
	if dir != "/var/lib/dclient" {
		return warn, dir + " (not /var/lib/dclient; vms will not be found by a root-run client)"
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

var osReleasePath = "/etc/os-release"

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
