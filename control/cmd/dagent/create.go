package main

import (
  "context"
  "errors"
  "fmt"
  "os"
  "time"

  "control/internal/proto"
)

// createRequest is one VM creation, in a form that survives being sent over the
// wire. The CLI builds it from flags; the daemon decodes it from JSON on its
// socket; the control server pushes it down the websocket. All three then call
// createVM, so there is exactly one implementation of what creating a VM means.
//
// It is an alias, not a conversion, because the control link and the local
// socket must not be able to drift into two subtly different request shapes.
type createRequest = proto.VMSpec

// createVM does the whole job: resolve images, build the disk, set up the
// network, start qemu. logf reports progress -- to the terminal for a direct
// run, or down the socket as NDJSON when the daemon is doing the work.
func createVM(ctx context.Context, data string, req createRequest, logf func(string, ...any)) (vm, error) {
  boot, err := parseBootMode(orDefault(req.Boot, string(bootDirect)))
  if err != nil {
    return vm{}, err
  }
  cpus, memory := req.CPUs, req.Memory
  if cpus == 0 {
    cpus = defaultCPUs
  }
  if memory == 0 {
    memory = defaultMemoryMiB
  }
  if cpus < 1 {
    return vm{}, errors.New("cpus must be at least 1")
  }
  if memory < 64 {
    return vm{}, errors.New("memory must be at least 64 MiB")
  }

  size, err := parseSize("--disk-size", req.DiskSize)
  if err != nil {
    return vm{}, err
  }
  rootfsSize, err := parseSize("--rootfs-size", req.RootfsSize)
  if err != nil {
    return vm{}, err
  }

  kernel := artifact{req.Kernel, req.KernelSHA}
  initrd := artifact{req.Initrd, req.InitrdSHA}
  rootfs := artifact{req.Rootfs, req.RootfsSHA}
  rootfsTar := artifact{req.RootfsTar, req.RootfsTarSHA}
  disk := artifact{req.Disk, req.DiskSHA}

  // The two boot modes take disjoint inputs; accepting the wrong ones silently
  // would mean booting something other than what was asked for.
  var backing artifact
  fromTar := false
  switch boot {
  case bootDirect:
    if kernel.empty() {
      return vm{}, fmt.Errorf("--kernel is required for %s boot", bootDirect)
    }
    if rootfs.empty() == rootfsTar.empty() {
      return vm{}, fmt.Errorf("%s boot needs exactly one of --rootfs or --rootfs-tar", bootDirect)
    }
    if !disk.empty() {
      return vm{}, fmt.Errorf("--disk is for %s boot; use --rootfs", bootDisk)
    }
    backing = rootfs
    if !rootfsTar.empty() {
      backing, fromTar = rootfsTar, true
    }
  case bootDisk:
    if disk.empty() {
      return vm{}, fmt.Errorf("--disk is required for %s boot", bootDisk)
    }
    if !kernel.empty() || !initrd.empty() || req.Append != "" {
      return vm{}, fmt.Errorf("--kernel, --initrd and --append are for %s boot", bootDirect)
    }
    if !rootfs.empty() || !rootfsTar.empty() {
      return vm{}, fmt.Errorf("--rootfs and --rootfs-tar are for %s boot; use --disk", bootDirect)
    }
    backing = disk
  }

  id, err := newVMID()
  if err != nil {
    return vm{}, err
  }
  name := req.Name
  if name == "" {
    name = id
  }

  // A uid to drop to is only useful if there is privilege to drop. Without it
  // every guest runs as whoever started dagent and shares that identity with
  // every other guest, which is worth saying out loud rather than leaving the
  // operator to infer it.
  uid := 0
  if os.Geteuid() == 0 {
    if uid, err = allocateUID(data); err != nil {
      return vm{}, err
    }
  } else {
    logf("WARNING: not root; this vm runs as the current user and is not isolated from other vms")
  }

  v := vm{
    ID:        id,
    Name:      name,
    CreatedAt: time.Now().UTC(),
    Boot:      boot,
    CPUs:      cpus,
    MemoryMiB: memory,
    Append:    req.Append,
    Firmware:  req.Firmware,
    Disk:      vmPath(data, id, vmOverlayImage),
    UID:       uid,
  }
  if boot == bootDirect && v.Append == "" {
    v.Append = defaultAppend()
  }

  cache := imagesDir(data)
  logf("resolving images")
  if v.Backing, err = backing.resolve(ctx, cache); err != nil {
    return vm{}, err
  }
  if fromTar {
    logf("building an ext4 rootfs from the tar (cached after the first time)")
    if v.Backing, err = ext4FromTar(ctx, cache, v.Backing, rootfsSize); err != nil {
      return vm{}, err
    }
  }
  if boot == bootDirect {
    if v.Kernel, err = kernel.resolve(ctx, cache); err != nil {
      return vm{}, err
    }
    if !initrd.empty() {
      if v.Initrd, err = initrd.resolve(ctx, cache); err != nil {
        return vm{}, err
      }
    }
  }

  // run/ has to exist before qemu does: it creates its sockets there and will
  // not create the directory itself.
  if err := os.MkdirAll(vmPath(data, id, vmRunDir), 0o700); err != nil {
    return vm{}, err
  }
  if uid != 0 {
    // The guest has to be able to reach its own directory to open its disk.
    if err := ensureTraversable(data); err != nil {
      return vm{}, err
    }
  }
  // Anything created from here on has to be undone if the boot fails, or the
  // operator is left with a half-made vm and a stale cgroup.
  cleanup := func() {
    _ = teardownVMNetwork(v)
    _ = removeCgroup(v.Cgroup)
    _ = os.RemoveAll(vmDir(data, id))
  }

  logf("creating the disk overlay")
  if err := newOverlay(ctx, v.Backing, v.Disk, size); err != nil {
    cleanup()
    return vm{}, err
  }

  // Networking, in the one order that is safe: interface, then policy, then the
  // guest. The VM must not be able to send a packet before the rules that
  // constrain it are already in the kernel.
  var tap *os.File
  if !req.NoNetwork {
    tap, err = setupVMNetwork(data, &v, netOptions{
      IP:        req.IP,
      Egress:    req.Egress,
      EgressAny: req.EgressAny,
      RateMbit:  req.RateMbit,
      BurstKbit: req.BurstKbit,
    })
    if err != nil {
      cleanup()
      return vm{}, err
    }
    defer tap.Close() // ours closes after exec; qemu holds the inherited copy
    logf("address %s on %s", v.Net.IP, v.Net.Tap)
  }

  if v.Cgroup, err = setupCgroup(v); err != nil {
    // Running unprivileged is a legitimate development mode; the guest just
    // does not get resource limits, and saying so is better than refusing.
    logf("WARNING: no cpu or memory limits will be applied: %v", err)
    v.Cgroup = ""
  }

  // Ownership last: everything qemu will open exists by now, and past this point
  // the files belong to the guest rather than to us.
  if err := chownVM(data, v); err != nil {
    cleanup()
    return vm{}, err
  }

  if uid != 0 {
    logf("running as uid %d", uid)
  }
  logf("starting qemu")
  pid, err := launchVM(ctx, data, &v, tap)
  if err != nil {
    cleanup()
    return vm{}, err
  }

  // Written after the boot so a vm.json on disk always describes something that
  // actually started.
  if err := saveVM(data, v); err != nil {
    _ = stopVM(ctx, data, id, defaultStopWait)
    cleanup()
    return vm{}, err
  }
  logf("vm %s (%s) started, pid %d", v.ID, v.Name, pid)
  return v, nil
}

// removeVM stops a VM and deletes everything belonging to it, in the reverse of
// the order it was created.
func removeVM(ctx context.Context, data string, v vm) error {
  if err := stopVM(ctx, data, v.ID, defaultStopWait); err != nil {
    return err
  }
  if err := teardownVMNetwork(v); err != nil {
    return err
  }
  if err := removeCgroup(v.Cgroup); err != nil {
    return err
  }
  // Only the vm's own directory goes: the cached base images are shared.
  return os.RemoveAll(vmDir(data, v.ID))
}

func orDefault(v, fallback string) string {
  if v == "" {
    return fallback
  }
  return v
}
