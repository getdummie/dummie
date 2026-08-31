// Package dinit is the guest side of dclient: the init a microvm boots when its
// rootfs came from a container image. Nothing in the image is required beyond a
// shell, so a plain debian:stable or alpine tar boots, gets its address and
// answers ssh.
//
// dclient copies this binary into the rootfs and boots it with init=; the
// address arrives on the kernel command line. Both halves of that contract are
// documented in README.md.
package dinit

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	// InitPath is where dclient installs this binary inside a rootfs.
	InitPath = "/sbin/dinit"

	// StateDir holds what the agent generates for itself: its ssh host key, and
	// authorized keys for images where root's home is managed elsewhere.
	StateDir = "/etc/dclient"

	ifaceName = "eth0"

	pathEnv = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

// The kernel command line dclient writes. systemd.hostname is reused rather than
// duplicated, so a handoff image and the agent read the same name.
const (
	paramIP       = "dclient.ip"
	paramGateway  = "dclient.gw"
	paramDNS      = "dclient.dns"
	paramHostname = "systemd.hostname"
)

var systemdPaths = []string{"/lib/systemd/systemd", "/usr/lib/systemd/systemd"}

var shells = []string{"/bin/bash", "/bin/sh", "/usr/bin/bash", "/usr/bin/sh", "/bin/busybox"}

func Run(version string) {
	if f, err := os.OpenFile("/dev/console", os.O_WRONLY, 0); err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(0)
	log.SetPrefix("dinit: ")
	log.Printf("starting (%s)", version)

	mount(earlyMounts)

	p := readParams("/proc/cmdline")
	if err := configureNet(p); err != nil {
		log.Printf("could not configure %s: %v", ifaceName, err)
	}
	writeIdentity(p)

	if systemd := findSystemd(); systemd != "" {
		execSystemd(systemd)
	}

	mount(agentMounts)
	runAgent()
}

type mountSpec struct {
	source, target, fstype, data string
	flags                        uintptr
}

var earlyMounts = []mountSpec{
	{source: "proc", target: "/proc", fstype: "proc", flags: unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC},
	{source: "sysfs", target: "/sys", fstype: "sysfs", flags: unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC},
	{source: "devtmpfs", target: "/dev", fstype: "devtmpfs", flags: unix.MS_NOSUID},
}

var agentMounts = []mountSpec{
	{source: "devpts", target: "/dev/pts", fstype: "devpts", data: "gid=5,mode=620,ptmxmode=666", flags: unix.MS_NOSUID | unix.MS_NOEXEC},
	{source: "tmpfs", target: "/dev/shm", fstype: "tmpfs", flags: unix.MS_NOSUID | unix.MS_NODEV},
	{source: "tmpfs", target: "/run", fstype: "tmpfs", flags: unix.MS_NOSUID | unix.MS_NODEV},
}

func mount(specs []mountSpec) {
	for _, m := range specs {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			log.Printf("could not create %s: %v", m.target, err)
			continue
		}
		err := unix.Mount(m.source, m.target, m.fstype, m.flags, m.data)
		if err != nil && err != unix.EBUSY {
			log.Printf("could not mount %s on %s: %v", m.fstype, m.target, err)
		}
	}
}

type params struct {
	ip, gateway, dns, hostname string
}

func readParams(path string) params {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Printf("could not read %s: %v", path, err)
		return params{}
	}
	var p params
	for _, f := range strings.Fields(string(b)) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case paramIP:
			p.ip = v
		case paramGateway:
			p.gateway = v
		case paramDNS:
			p.dns = v
		case paramHostname:
			p.hostname = v
		}
	}
	return p
}

func configureNet(p params) error {
	if p.ip == "" {
		return nil
	}
	ip := net.ParseIP(p.ip)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("%s=%q is not an ipv4 address", paramIP, p.ip)
	}

	link, err := ethernetLink()
	if err != nil {
		return err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: ip.To4(), Mask: net.CIDRMask(32, 32)}}
	if err := netlink.AddrReplace(link, addr); err != nil {
		return err
	}
	log.Printf("%s: %s/32", link.Attrs().Name, ip)

	if p.gateway == "" {
		return nil
	}
	gw := net.ParseIP(p.gateway)
	if gw == nil || gw.To4() == nil {
		return fmt.Errorf("%s=%q is not an ipv4 address", paramGateway, p.gateway)
	}
	// The address is a /32, so the gateway needs its own on-link route before it
	// can carry the default one.
	onlink := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       &net.IPNet{IP: gw.To4(), Mask: net.CIDRMask(32, 32)},
		Scope:     netlink.SCOPE_LINK,
	}
	if err := netlink.RouteReplace(onlink); err != nil {
		return err
	}
	if err := netlink.RouteReplace(&netlink.Route{LinkIndex: link.Attrs().Index, Gw: gw.To4()}); err != nil {
		return err
	}
	log.Printf("default via %s", gw)
	return nil
}

func ethernetLink() (netlink.Link, error) {
	if link, err := netlink.LinkByName(ifaceName); err == nil {
		return link, nil
	}
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.Attrs().Name != "lo" && len(l.Attrs().HardwareAddr) > 0 {
			return l, nil
		}
	}
	return nil, fmt.Errorf("the guest has no ethernet interface")
}

func writeIdentity(p params) {
	if p.hostname != "" {
		if err := unix.Sethostname([]byte(p.hostname)); err != nil {
			log.Printf("could not set the hostname: %v", err)
		}
		writeFile("/etc/hostname", p.hostname+"\n")
		ensureHosts("/etc/hosts", p.hostname, p.ip)
	}
	if p.dns != "" {
		writeFile("/etc/resolv.conf", "# Written by dinit.\nnameserver "+p.dns+"\n")
	}
}

// writeFile leaves symlinks alone: an image that points /etc/resolv.conf at
// systemd-resolved manages the file itself.
func writeFile(path, body string) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		log.Printf("could not write %s: %v", path, err)
	}
}

func ensureHosts(path, hostname, ip string) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	body := string(b)
	if strings.Contains(body, " "+hostname) || strings.Contains(body, "\t"+hostname) {
		return
	}
	if body == "" {
		body = "127.0.0.1\tlocalhost\n::1\tlocalhost\n"
	} else if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if ip == "" {
		ip = "127.0.1.1"
	}
	writeFile(path, body+ip+"\t"+hostname+"\n")
}

// findSystemd decides between the two modes. Only systemd counts: busybox's
// /sbin/init brings up nothing without an inittab, so those images are better
// served by the agent.
func findSystemd() string {
	for _, p := range systemdPaths {
		if executable(p) {
			return p
		}
	}
	return ""
}

// execSystemd hands pid 1 over to an image that brings up its own services. It
// only returns if the exec fails.
func execSystemd(path string) {
	log.Printf("handing off to %s", path)
	runtime.LockOSThread()
	signal.Reset()
	var empty unix.Sigset_t
	if err := unix.PthreadSigmask(unix.SIG_SETMASK, &empty, nil); err != nil {
		log.Printf("could not clear the signal mask: %v", err)
	}
	if err := unix.Exec(path, []string{path}, []string{pathEnv, "TERM=linux"}); err != nil {
		log.Printf("could not exec %s (%v); staying on as the guest init", path, err)
	}
	runtime.UnlockOSThread()
}

func runAgent() {
	r := newReaper()
	r.start()

	go serveSSH(r)
	go superviseConsole(r)

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, unix.SIGTERM, unix.SIGINT)
	s := <-sig
	log.Printf("%s: powering off", s)
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF); err != nil {
		_ = unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART)
	}
}

func superviseConsole(r *reaper) {
	shell := findShell()
	if shell == "" {
		log.Print("no shell in the image; the serial console will stay empty")
		return
	}
	for {
		pid, err := startConsoleShell(r, shell)
		if err != nil {
			log.Printf("could not start a console shell: %v", err)
			return
		}
		<-r.wait(pid)
		time.Sleep(time.Second)
	}
}

func startConsoleShell(r *reaper, shell string) (int, error) {
	con, err := os.OpenFile("/dev/console", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return 0, err
	}
	defer con.Close()

	return r.spawn(shell, shellArgv(shell, nil), &syscall.ProcAttr{
		Dir:   "/root",
		Env:   []string{pathEnv, "TERM=vt220", "HOME=/root", "USER=root"},
		Files: []uintptr{con.Fd(), con.Fd(), con.Fd()},
		Sys:   &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0},
	})
}

func findShell() string {
	for _, s := range shells {
		if executable(s) {
			return s
		}
	}
	return ""
}

// shellArgv makes the shell a login shell when the session asked for one.
// busybox dispatches on argv[0], so it is invoked as sh instead.
func shellArgv(shell string, args []string) []string {
	name := baseName(shell)
	if name == "busybox" {
		name = "sh"
	} else if len(args) == 0 {
		name = "-" + name
	}
	return append([]string{name}, args...)
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// reaper is the part of being pid 1 that cannot be skipped: every orphan in the
// guest is reparented here, and the sessions that care about an exit status read
// it from the same wait4 loop.
type reaper struct {
	mu      sync.Mutex
	waiters map[int]chan syscall.WaitStatus
	exited  map[int]syscall.WaitStatus
	tracked map[int]bool
}

func newReaper() *reaper {
	return &reaper{
		waiters: map[int]chan syscall.WaitStatus{},
		exited:  map[int]syscall.WaitStatus{},
		tracked: map[int]bool{},
	}
}

func (r *reaper) start() {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, unix.SIGCHLD)
	go func() {
		for range ch {
			r.collect()
		}
	}()
	// SIGCHLD coalesces, so sweep periodically as well.
	go func() {
		for {
			time.Sleep(5 * time.Second)
			r.collect()
		}
	}()
}

func (r *reaper) collect() {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if err != nil || pid <= 0 {
			return
		}
		r.deliver(pid, ws)
	}
}

func (r *reaper) deliver(pid int, ws syscall.WaitStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.tracked[pid] {
		return
	}
	delete(r.tracked, pid)
	if w, ok := r.waiters[pid]; ok {
		delete(r.waiters, pid)
		w <- ws
		return
	}
	r.exited[pid] = ws
}

// spawn holds the lock across the fork so a child that exits immediately cannot
// be reaped before it is tracked.
func (r *reaper) spawn(path string, argv []string, attr *syscall.ProcAttr) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pid, err := syscall.ForkExec(path, argv, attr)
	if err != nil {
		return 0, err
	}
	r.tracked[pid] = true
	return pid, nil
}

func (r *reaper) wait(pid int) <-chan syscall.WaitStatus {
	ch := make(chan syscall.WaitStatus, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ws, ok := r.exited[pid]; ok {
		delete(r.exited, pid)
		ch <- ws
		return ch
	}
	r.waiters[pid] = ch
	return ch
}

func (r *reaper) signal(pid int, sig syscall.Signal) {
	r.mu.Lock()
	tracked := r.tracked[pid]
	r.mu.Unlock()
	if tracked {
		_ = syscall.Kill(-pid, sig)
	}
}
