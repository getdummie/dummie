package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

const (
	uidBase = 2_000_000
	uidCount = 65536
)

func allocateUID(data string) (int, error) {
	vms, err := listVMs(data)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]bool, len(vms))
	for _, v := range vms {
		if v.UID != 0 {
			taken[v.UID] = true
		}
	}

	for uid := uidBase; uid < uidBase+uidCount; uid++ {
		if taken[uid] {
			continue
		}
		if _, err := user.LookupId(strconv.Itoa(uid)); err == nil {
			continue
		}
		return uid, nil
	}
	return 0, fmt.Errorf("no free uid in %d-%d", uidBase, uidBase+uidCount-1)
}

func vmCredential(v vm) (*syscall.Credential, bool) {
	if v.UID == 0 {
		return nil, kvmAvailable()
	}
	groups, kvm := kvmAccess()
	return &syscall.Credential{
		Uid:    uint32(v.UID),
		Gid:    uint32(v.UID),
		Groups: groups,
	}, kvm
}

func kvmAccess() (groups []uint32, usable bool) {
	info, err := os.Stat("/dev/kvm")
	if err != nil {
		return nil, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, false
	}

	mode := info.Mode().Perm()
	if mode&0o006 == 0o006 {
		return nil, true
	}
	if mode&0o060 == 0o060 && st.Gid != 0 {
		return []uint32{st.Gid}, true
	}
	return nil, false
}

const kvmGroup = "kvm"

func ensureKVMAccess() (string, error) {
	if _, usable := kvmAccess(); usable {
		return "/dev/kvm is already reachable by an unprivileged uid", nil
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		return "/dev/kvm is missing; vms will run under software emulation", nil
	}

	g, err := user.LookupGroup(kvmGroup)
	if err != nil {
		out, aerr := exec.Command("groupadd", "--system", kvmGroup).CombinedOutput()
		if aerr != nil {
			return "", fmt.Errorf("could not create the %s group: %v: %s", kvmGroup, aerr, out)
		}
		if g, err = user.LookupGroup(kvmGroup); err != nil {
			return "", err
		}
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return "", fmt.Errorf("the %s group has a non-numeric gid %q", kvmGroup, g.Gid)
	}

	if err := os.Chown("/dev/kvm", 0, gid); err != nil {
		return "", fmt.Errorf("could not set the group on /dev/kvm: %w", err)
	}
	if err := os.Chmod("/dev/kvm", 0o660); err != nil {
		return "", fmt.Errorf("could not set the mode on /dev/kvm: %w", err)
	}
	return fmt.Sprintf("/dev/kvm set to root:%s (gid %d) mode 0660", kvmGroup, gid), nil
}

func chownVM(data string, v vm) error {
	if v.UID == 0 {
		return nil
	}
	dir := vmDir(data, v.ID)
	if err := os.Lchown(dir, 0, v.UID); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o710); err != nil {
		return err
	}
	if err := os.Chmod(v.Disk, 0o600); err != nil {
		return err
	}
	for _, path := range []string{v.Disk, vmPath(data, v.ID, vmRunDir)} {
		if err := os.Lchown(path, v.UID, v.UID); err != nil {
			return err
		}
	}
	return nil
}

func ensureTraversable(data string) error {
	for _, dir := range []string{data, vmsDir(data)} {
		if err := os.MkdirAll(dir, 0o711); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o711); err != nil {
			return err
		}
	}
	return nil
}
