package main

import (
  "context"
  "errors"
  "fmt"
  "os"
  "time"
)

// createRequest is one VM creation, in a form that survives being sent over the
// wire. The CLI builds it from flags; the daemon decodes it from JSON. Both
// then call createVM, so there is exactly one implementation of what creating a
// VM means.
type createRequest struct {
  Name string `json:"name,omitempty"`
  Boot string `json:"boot,omitempty"`

  Kernel        string `json:"kernel,omitempty"`
  KernelSHA     string `json:"kernel_sha256,omitempty"`
  Initrd        string `json:"initrd,omitempty"`
  InitrdSHA     string `json:"initrd_sha256,omitempty"`
  Rootfs        string `json:"rootfs,omitempty"`
  RootfsSHA     string `json:"rootfs_sha256,omitempty"`
  RootfsTar     string `json:"rootfs_tar,omitempty"`
  RootfsTarSHA  string `json:"rootfs_tar_sha256,omitempty"`
  RootfsSize    string `json:"rootfs_size,omitempty"`
  Disk          string `json:"disk,omitempty"`
  DiskSHA       string `json:"disk_sha256,omitempty"`
  DiskSize      string `json:"disk_size,omitempty"`
  Append        string `json:"append,omitempty"`
  Firmware      string `json:"firmware,omitempty"`

  CPUs   int `json:"cpus,omitempty"`
  Memory int `json:"memory_mib,omitempty"`

  NoNetwork bool     `json:"no_network,omitempty"`
  IP        string   `json:"ip,omitempty"`
  Egress    []string `json:"egress,omitempty"`
  EgressAny bool     `json:"egress_any,omitempty"`
  RateMbit  int      `json:"rate_mbit,omitempty"`
  BurstKbit int      `json:"burst_kbit,omitempty"`
}

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

  if err := os.MkdirAll(vmDir(data, id), 0o700); err != nil {
    return vm{}, err
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
