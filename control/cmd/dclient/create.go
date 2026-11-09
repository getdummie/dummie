package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"control/internal/proto"
)

type createRequest = proto.VMSpec

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
		DiskBytes: size,
		UID:       uid,
	}
	if boot == bootDirect {
		if v.Append == "" {
			v.Append = defaultAppend()
		}
		v.Append = withHostname(v.Append, name)
	}

	if req.FSx != nil && !fromTar {
		return vm{}, errors.New("an fsx disk is built from a rootfs tar; use --rootfs-tar")
	}

	cache := imagesDir(data)
	logf("resolving images")
	if fromTar {
		if v.Backing, v.GuestInit, err = buildRootfs(ctx, data, backing, req.Image, rootfsSize, logf); err != nil {
			return vm{}, err
		}
		if v.GuestInit {
			v.Append = withGitIdentity(v.Append, req.Git)
		}
	} else if v.Backing, err = backing.resolve(ctx, cache); err != nil {
		return vm{}, err
	}
	if req.FSx != nil {
		fsx := *req.FSx
		v.FSx = &fsx
		v.Disk = fsxDiskPath(data, id)
		v.Backing = ""
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

	if err := os.MkdirAll(vmPath(data, id, vmRunDir), 0o700); err != nil {
		return vm{}, err
	}
	if uid != 0 {
		if err := ensureTraversable(data); err != nil {
			return vm{}, err
		}
	}
	cleanup := func() {
		_ = teardownVMNetwork(v)
		_ = removeCgroup(v.Cgroup)
		_ = removeVMDir(context.WithoutCancel(ctx), data, v)
	}

	if v.FSx != nil {
		logf("mounting %s:%s", v.FSx.Server, v.FSx.Path)
		if err := mountFSx(ctx, *v.FSx, fsxMountPoint(data, v)); err != nil {
			cleanup()
			return vm{}, err
		}
		if _, err := growDisk(ctx, v); err != nil {
			cleanup()
			return vm{}, err
		}
	} else {
		logf("creating the disk overlay")
		if err := newOverlay(ctx, v.Backing, v.Disk, size); err != nil {
			cleanup()
			return vm{}, err
		}
	}

	if req.InitScript != "" {
		if !v.GuestInit {
			cleanup()
			return vm{}, errors.New("an init script needs a rootfs booted by dinit")
		}
		v.InitDisk = vmPath(data, id, vmInitDisk)
		if err := writeInitDisk(v.InitDisk, req.InitScript); err != nil {
			cleanup()
			return vm{}, err
		}
	}

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
		defer tap.Close()
		logf("address %s on %s", v.Net.IP, v.Net.Tap)
	}

	if v.Cgroup, err = setupCgroup(v); err != nil {
		logf("WARNING: no cpu or memory limits will be applied: %v", err)
		v.Cgroup = ""
	}

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

	if err := saveVM(data, v); err != nil {
		_ = stopVM(ctx, data, id, defaultStopWait)
		cleanup()
		return vm{}, err
	}
	logf("vm %s (%s) started, pid %d", v.ID, v.Name, pid)
	return v, nil
}

func startVM(ctx context.Context, data string, v vm, logf func(string, ...any)) (int, error) {
	if pid := vmPID(data, v.ID); pid != 0 {
		return pid, nil
	}

	if err := os.MkdirAll(vmPath(data, v.ID, vmRunDir), 0o700); err != nil {
		return 0, err
	}
	for _, sock := range []string{vmQMPSocket, vmConsoleSock} {
		if err := os.Remove(vmPath(data, v.ID, sock)); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}

	if v.FSx != nil {
		if err := mountFSx(ctx, *v.FSx, fsxMountPoint(data, v)); err != nil {
			return 0, err
		}
	}
	if grown, err := growDisk(ctx, v); err != nil {
		return 0, err
	} else if grown {
		logf("grew the disk to %d MiB; the guest grows its filesystem itself", v.DiskBytes>>20)
	}

	var tap *os.File
	var err error
	if v.Net != nil {
		if tap, err = restoreVMNetwork(data, &v); err != nil {
			return 0, err
		}
		defer tap.Close()
		logf("address %s on %s", v.Net.IP, v.Net.Tap)
	}

	cleanup := func() {
		_ = teardownVMNetwork(v)
		_ = removeCgroup(v.Cgroup)
	}

	if v.Cgroup, err = setupCgroup(v); err != nil {
		logf("WARNING: no cpu or memory limits will be applied: %v", err)
		v.Cgroup = ""
	}
	if err := chownVM(data, v); err != nil {
		cleanup()
		return 0, err
	}

	logf("starting qemu")
	pid, err := launchVM(ctx, data, &v, tap)
	if err != nil {
		cleanup()
		return 0, err
	}

	if err := saveVM(data, v); err != nil {
		_ = stopVM(ctx, data, v.ID, defaultStopWait)
		cleanup()
		return 0, err
	}
	logf("vm %s (%s) started, pid %d", v.ID, v.Name, pid)
	return pid, nil
}

// resizeVM records a new size for a vm that already exists. Nothing is applied
// to the running guest: qemu reads cpus and memory when it launches, and the
// disk is grown on the next start, so the owner has to restart the vm for any
// of this to take effect.
func resizeVM(ctx context.Context, data string, v vm, req proto.VMResize) error {
	if req.CPUs < 1 {
		return errors.New("cpus must be at least 1")
	}
	if req.Memory < 64 {
		return errors.New("memory must be at least 64 MiB")
	}
	size, err := parseSize("--disk-size", req.DiskSize)
	if err != nil {
		return err
	}
	// The recorded size is what the image is asked to be, so a size that is not
	// moving needs no look at the image at all -- and a vm whose size was never
	// recorded is the only case worth opening it for.
	if size > 0 && size != v.DiskBytes {
		current := v.DiskBytes
		if current == 0 && v.FSx != nil {
			if err := mountFSx(ctx, *v.FSx, fsxMountPoint(data, v)); err != nil {
				return err
			}
		}
		if current == 0 {
			if current, err = imageVirtualSize(ctx, v.Disk); err != nil {
				return err
			}
		}
		if size < current {
			return fmt.Errorf("a disk can only grow: this vm already has %d MiB", current>>20)
		}
		v.DiskBytes = size
	}
	if kernel := (artifact{req.Kernel, req.KernelSHA}); !kernel.empty() {
		if v.Boot != bootDirect {
			return fmt.Errorf("only a %s-boot vm takes a kernel", bootDirect)
		}
		if v.Kernel, err = kernel.resolve(ctx, imagesDir(data)); err != nil {
			return err
		}
	}
	v.CPUs, v.MemoryMiB = req.CPUs, req.Memory
	return saveVM(data, v)
}

// growDisk brings the overlay up to the size the vm asks for, and reports
// whether it had to. It runs with no qemu holding the image, which is the only
// time growing it is safe.
func growDisk(ctx context.Context, v vm) (bool, error) {
	if v.DiskBytes <= 0 {
		return false, nil
	}
	current, err := imageVirtualSize(ctx, v.Disk)
	if err != nil {
		return false, err
	}
	if current >= v.DiskBytes {
		return false, nil
	}
	if v.FSx != nil {
		return true, os.Truncate(v.Disk, v.DiskBytes)
	}
	return true, resizeImage(ctx, v.Disk, v.DiskBytes)
}

// buildRootfs fetches a rootfs tar and turns it into the cached ext4 a vm boots,
// reporting whether dinit was installed into it.
func buildRootfs(ctx context.Context, data string, tar artifact, image *proto.ImageConfig, sizeBytes int64, logf func(string, ...any)) (string, bool, error) {
	cache := imagesDir(data)
	path, err := tar.resolve(ctx, cache)
	if err != nil {
		return "", false, err
	}
	pubKey, err := readClientPubKey()
	if err != nil {
		return "", false, err
	}
	if pubKey == "" {
		logf("no dpipe key at %s; this vm will not accept dpipe's ssh key", clientPubKeyPath)
	}
	cfg, err := loadNetConfig(data)
	if err != nil {
		return "", false, err
	}
	logf("building an ext4 rootfs from the tar (cached after the first time)")
	return ext4FromTar(ctx, cache, path, pubKey, cfg.resolver(), image, sizeBytes, logf)
}

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
	return removeVMDir(ctx, data, v)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
