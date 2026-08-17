package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// startupGrace is how long a freshly started qemu is watched. Bad arguments and
// missing firmware surface within milliseconds, so a process still alive after
// this has, for our purposes, started.
const startupGrace = 1500 * time.Millisecond

// tapChildFD is where the tap lands in the child. Go's exec dups ExtraFiles
// starting at 3, and the tap is the only file we pass.
const tapChildFD = 3

func qemuBinary() string {
	if runtime.GOARCH == "arm64" {
		return "qemu-system-aarch64"
	}
	return "qemu-system-x86_64"
}

// kvmAvailable reports whether this machine can run guests natively. Without it
// qemu still works via TCG emulation, just slowly, so this is not fatal.
func kvmAvailable() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// defaultAppend is the kernel command line used when the operator gives none.
// The console must be named explicitly: with -nodefaults the guest has no other
// way to find the serial port we attached.
func defaultAppend() string {
	console := "ttyS0"
	if runtime.GOARCH == "arm64" {
		console = "ttyAMA0"
	}
	return fmt.Sprintf("console=%s root=/dev/vda rw reboot=k panic=1", console)
}

// qemuArgs builds the whole command line. Everything the VM can touch is named
// here explicitly -- -nodefaults plus -no-user-config means qemu adds nothing of
// its own, so this list is the complete hardware inventory of the guest.
func qemuArgs(data string, v vm, kvm bool) []string {
	machine, mmio := machineType(v.Boot)

	args := []string{
		"-name", "dclient-" + v.ID,
		"-no-user-config",
		"-nodefaults",
		"-display", "none",
		"-no-reboot",
		"-machine", machine,
		"-smp", fmt.Sprintf("%d", v.CPUs),
		"-m", fmt.Sprintf("%d", v.MemoryMiB),
	}

	if kvm {
		args = append(args, "-enable-kvm", "-cpu", "host")
	} else {
		args = append(args, "-cpu", "max")
	}

	// Seccomp: qemu confines itself to the syscalls it needs. spawn=deny in
	// particular means a compromised qemu cannot exec anything.
	args = append(args, "-sandbox", "on,obsolete=deny,elevateprivileges=deny,spawn=deny,resourcecontrol=deny")

	if v.Net == nil {
		args = append(args, "-nic", "none")
	} else {
		// The tap arrives as an already-open fd, inherited across exec. QEMU never
		// opens /dev/net/tun, never names an interface, and so needs no
		// CAP_NET_ADMIN -- it cannot reconfigure the network even if compromised.
		args = append(args,
			"-netdev", fmt.Sprintf("tap,id=net0,fd=%d,vhost=off", tapChildFD),
			"-device", device("virtio-net", mmio)+",netdev=net0,mac="+v.Net.MAC,
		)
	}

	if v.Firmware != "" {
		args = append(args, "-bios", v.Firmware)
	}

	args = append(args,
		"-qmp", "unix:"+vmPath(data, v.ID, vmQMPSocket)+",server=on,wait=off",
		"-chardev", "socket,id=con0,path="+vmPath(data, v.ID, vmConsoleSock)+
			",server=on,wait=off,logfile="+vmPath(data, v.ID, vmConsoleLog)+",logappend=on",
		"-serial", "chardev:con0",
	)

	args = append(args,
		"-drive", "file="+v.Disk+",format=qcow2,if=none,id=root",
		"-device", device("virtio-blk", mmio)+",drive=root",
		"-device", device("virtio-rng", mmio),
	)

	if v.Boot == bootDirect {
		args = append(args, "-kernel", v.Kernel)
		if v.Initrd != "" {
			args = append(args, "-initrd", v.Initrd)
		}
		args = append(args, "-append", v.Append)
	}

	return args
}

// machineType also reports whether virtio devices sit on an mmio bus (microvm,
// virt) or on PCI (q35) -- the device names differ between the two.
func machineType(boot bootMode) (machine string, mmio bool) {
	if runtime.GOARCH == "arm64" {
		return "virt", true
	}
	if boot == bootDirect {
		return "microvm", true
	}
	return "q35", false
}

func device(base string, mmio bool) string {
	if mmio {
		return base + "-device"
	}
	return base + "-pci"
}

// launchVM starts qemu detached and returns its pid. The caller's exit must not
// take the guest with it, so the process gets its own session and its output
// goes to a file rather than to our stdio.
//
// This is also where the VM stops being privileged: the credential is applied
// between fork and exec, so the qemu that comes out the other side has never run
// as anything but its own uid.
func launchVM(ctx context.Context, data string, v *vm, tap *os.File) (int, error) {
	logFile, err := os.OpenFile(vmPath(data, v.ID, vmQEMULog), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()

	cred, kvm := vmCredential(*v)
	if cred != nil && !kvm && kvmAvailable() {
		// Worth a line: the guest is about to be an order of magnitude slower, and
		// the reason is a device permission rather than anything about the VM.
		log.Printf("vm %s: /dev/kvm is not reachable by uid %d; falling back to software emulation",
			v.ID, v.UID)
	}
	args := qemuArgs(data, *v, kvm)

	cgFD := -1
	if v.Cgroup != "" {
		fd, err := os.Open(v.Cgroup)
		if err != nil {
			return 0, err
		}
		defer fd.Close()
		cgFD = int(fd.Fd())
	}

	start := func(useCgroup bool) (*exec.Cmd, error) {
		cmd := exec.Command(qemuBinary(), args...)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if tap != nil {
			cmd.ExtraFiles = []*os.File{tap}
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Credential: cred}
		if useCgroup {
			cmd.SysProcAttr.UseCgroupFD = true
			cmd.SysProcAttr.CgroupFD = cgFD
		}
		return cmd, cmd.Start()
	}

	cmd, err := start(cgFD >= 0)
	if err != nil && cgFD >= 0 {
		// Spawning straight into a cgroup needs clone3 (Linux 5.7+). Falling back
		// means the limits are applied a moment late, which is worth having over
		// refusing to boot.
		log.Printf("could not start inside the cgroup (%v); starting without it", err)
		v.Cgroup = ""
		cmd, err = start(false)
	}
	if err != nil {
		return 0, fmt.Errorf("could not start %s: %w", qemuBinary(), err)
	}

	pid := cmd.Process.Pid
	if err := writePID(data, v.ID, pid); err != nil {
		_ = cmd.Process.Kill()
		return 0, err
	}

	// Reaped in the background so an immediate failure is observed rather than
	// left as a zombie; if it survives the grace period we exit and init adopts
	// it.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	select {
	case werr := <-exited:
		return 0, fmt.Errorf("%s exited immediately (%v); see %s:\n%s",
			qemuBinary(), werr, vmPath(data, v.ID, vmQEMULog), tailFile(vmPath(data, v.ID, vmQEMULog), 2048))
	case <-time.After(startupGrace):
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return 0, ctx.Err()
	}

	return pid, nil
}

// stopVM asks the guest to power down, then insists. A guest that ignores ACPI
// -- or has not booted far enough to see it -- must not leave a VM wedged.
func stopVM(ctx context.Context, data, id string, timeout time.Duration) error {
	pid := vmPID(data, id)
	if pid == 0 {
		return nil
	}

	if err := qmpCommand(ctx, vmPath(data, id, vmQMPSocket), "system_powerdown"); err != nil {
		log.Printf("qmp powerdown failed (%v); sending SIGTERM", err)
		_ = signalVM(data, id, syscall.SIGTERM)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if vmPID(data, id) == 0 {
			return os.Remove(vmPath(data, id, vmPIDFile))
		}
		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("vm %s did not shut down within %s; killing", id, timeout)
	if err := signalVM(data, id, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	time.Sleep(200 * time.Millisecond)
	return os.Remove(vmPath(data, id, vmPIDFile))
}

// tailFile returns the last n bytes, for putting a failure in front of the
// operator instead of a path to go and read.
func tailFile(p string, n int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > n {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return ""
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
