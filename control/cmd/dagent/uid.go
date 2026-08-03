package main

import (
  "fmt"
  "os"
  "os/exec"
  "os/user"
  "strconv"
  "syscall"
)

// Every VM runs as a uid of its own. The rest of the confinement was already
// here -- seccomp, a cgroup, the tap handed over as an open fd so qemu never
// needs CAP_NET_ADMIN -- but all of it ran as one user, so a qemu escape landed
// on the identity that owned every other guest's disk. The uid is what makes the
// blast radius of an escape one machine instead of the fleet.
//
// These uids are not accounts. Nothing is written to /etc/passwd: the kernel
// compares numbers, and a real account would be a login the operator never
// asked for. Each one exists as the owner of a single directory and the
// credential of a single process, and nothing ever resolves it to a name.
const (
  // uidBase is the bottom of the range. It sits clear of the ranges other
  // things claim: a host's own users are almost always below 10000, systemd's
  // dynamic users below 65536, and useradd hands out subuid maps from 100000.
  // Allocation also skips any number that resolves to a real account, so a host
  // that does use this range loses nothing but the collisions.
  uidBase = 2_000_000
  // uidCount bounds the range so an operator can reserve it in one line. It is
  // far more VMs than either the address pool or the host's memory allows.
  uidCount = 65536
)

// allocateUID picks the lowest free uid. Like allocateIP, the answer is derived
// from the VMs on disk rather than from a counter, so there is no second source
// of truth to drift -- and a stopped VM keeps its uid, because its files are
// still sitting there owned by it.
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
    // An account at this number would mean handing a guest a real user's
    // identity, and with it every file that user owns. Note that this only sees
    // what NSS will resolve: a binary built without cgo reads /etc/passwd and
    // nothing else, so a directory service using this range is not detected.
    if _, err := user.LookupId(strconv.Itoa(uid)); err == nil {
      continue
    }
    return uid, nil
  }
  return 0, fmt.Errorf("no free uid in %d-%d", uidBase, uidBase+uidCount-1)
}

// vmCredential is the identity qemu is exec'd with, and whether that identity
// can still reach /dev/kvm.
//
// Privilege is dropped by the parent between fork and exec rather than by qemu's
// own -runas. Two things fall out of that: qemu never runs as root even briefly,
// and it needs none of the setuid syscalls its own seccomp policy
// (elevateprivileges=deny) is there to forbid.
//
// A VM with no uid of its own is the unprivileged development path, where there
// was no privilege to drop in the first place. It runs as dagent does.
func vmCredential(v vm) (*syscall.Credential, bool) {
  if v.UID == 0 {
    return nil, kvmAvailable()
  }
  groups, kvm := kvmAccess()
  // Gid mirrors uid: one group per VM, existing for the same reason and, like
  // the uid, never written to /etc/group. Leaving Groups otherwise empty is
  // what makes the exec drop root's supplementary groups as well.
  return &syscall.Credential{
    Uid:    uint32(v.UID),
    Gid:    uint32(v.UID),
    Groups: groups,
  }, kvm
}

// kvmAccess reports how an unprivileged uid reaches /dev/kvm, and whether it can
// at all. This has to be asked separately from kvmAvailable: that one answers
// for dagent, which is root, and the guest is no longer.
//
// The device's own ownership is what gets checked rather than the name of a
// group. It is "kvm" on Debian and Ubuntu, but that is a convention.
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
    return nil, true // world-writable: no group membership needed
  }
  // Joining root's group to reach one device would hand the guest every
  // root-group-readable file on the host. Software emulation is the better
  // trade, and the caller says so out loud when it takes it.
  if mode&0o060 == 0o060 && st.Gid != 0 {
    return []uint32{st.Gid}, true
  }
  return nil, false
}

// kvmGroup is the group /dev/kvm is handed to when dagent has to repair the
// device itself. The name is the Debian and Ubuntu convention; the gid is
// whatever the host already uses, or whatever groupadd picks.
const kvmGroup = "kvm"

// ensureKVMAccess gives /dev/kvm a group that an unprivileged uid can be put
// into, and reports what it did.
//
// This exists because /dev is not always managed by anything that would set the
// group: a container's /dev is built by the runtime, and an image with no udev
// installed has no rules to apply. Either way the device is left 0600 root:root,
// which since VMs stopped running as root means every guest silently falls back
// to software emulation. /dev is also rebuilt on every boot, so a hand-applied
// chgrp does not survive -- which is why the daemon calls this at startup rather
// than trusting the install to have done it once.
//
// root:kvm 0660 is what Debian and Ubuntu ship, so this restores the distro
// default rather than loosening past it, and it does nothing at all if some group
// already has access.
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

// chownVM hands the guest exactly two things: the overlay disk it boots from,
// and the run directory it creates its sockets in. Everything else in the VM's
// directory -- vm.json above all -- stays root-owned in a directory the guest
// cannot write to, because a guest that can rewrite vm.json is a guest that can
// write its own egress policy the next time the reconciler reads it.
//
// The VM's directory itself becomes root:<its own gid>, mode 0710: root owns it
// so the guest cannot write to it, and only the one gid can traverse it, so no
// other guest can even enumerate what is inside. The disk goes to 0600 on top of
// that, because two defences that fail differently are worth more than one.
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
  // Lchown, so a symlink cannot be used to aim the chown somewhere else.
  for _, path := range []string{v.Disk, vmPath(data, v.ID, vmRunDir)} {
    if err := os.Lchown(path, v.UID, v.UID); err != nil {
      return err
    }
  }
  return nil
}

// ensureTraversable opens the path down to the vms directory by exactly one bit:
// execute for everyone, read for nobody. That is all qemu needs -- it reaches
// its disk and its sockets by absolute path, never by listing -- and it is as
// far as the loosening goes. Which VM directory a guest may then enter is
// decided one level down, by the gid chownVM puts on each one.
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
