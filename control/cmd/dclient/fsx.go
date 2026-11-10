package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"control/internal/proto"
)

const (
	vmFSxMount = "disk"
	fsxDiskRaw = "disk.raw"

	fsxMountOptions = "nfsvers=4.1,hard,nconnect=16,timeo=600,retrans=2"
)

var rootfsCacheName = regexp.MustCompile(`^[0-9a-f]{32}-rootfs\.ext4$`)

func fsxMountPoint(data string, v vm) string { return vmPath(data, v.ID, vmFSxMount) }

func fsxDiskPath(data, id string) string {
	return filepath.Join(vmPath(data, id, vmFSxMount), fsxDiskRaw)
}

func mountFSx(ctx context.Context, fsx proto.FSxDisk, target string) error {
	if fsx.Server == "" || !strings.HasPrefix(fsx.Path, "/") {
		return fmt.Errorf("an fsx disk needs a server and an absolute path, got %q:%q", fsx.Server, fsx.Path)
	}
	if err := os.MkdirAll(target, 0o711); err != nil {
		return err
	}
	if mounted, err := isMountPoint(target); err != nil || mounted {
		return err
	}
	out, err := exec.CommandContext(ctx, "mount", "-t", "nfs", "-o", fsxMountOptions,
		fsx.Server+":"+fsx.Path, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not mount %s:%s: %v: %s", fsx.Server, fsx.Path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func unmountFSx(ctx context.Context, target string) error {
	mounted, err := isMountPoint(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !mounted {
		return nil
	}
	out, err := exec.CommandContext(ctx, "umount", target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not unmount %s: %v: %s", target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isMountPoint(dir string) (bool, error) {
	var self, parent syscall.Stat_t
	if err := syscall.Stat(dir, &self); err != nil {
		return false, &os.PathError{Op: "stat", Path: dir, Err: err}
	}
	if err := syscall.Stat(filepath.Dir(dir), &parent); err != nil {
		return false, err
	}
	return self.Dev != parent.Dev, nil
}

// removeVMDir refuses to touch a vm directory that still has its volume
// mounted: RemoveAll would walk into it and delete the disk on FSx.
func removeVMDir(ctx context.Context, data string, v vm) error {
	if v.FSx != nil {
		target := fsxMountPoint(data, v)
		if err := unmountFSx(ctx, target); err != nil {
			return err
		}
		if mounted, err := isMountPoint(target); err == nil && mounted {
			return fmt.Errorf("%s is still mounted; not removing the vm directory", target)
		}
	}
	return os.RemoveAll(vmDir(data, v.ID))
}

func prepareRootfs(ctx context.Context, data string, req proto.VMSpec, logf func(string, ...any)) (proto.PreparedRootfs, error) {
	if req.RootfsTar == "" {
		return proto.PreparedRootfs{}, errors.New("an fsx disk is built from a rootfs tar, and this spec has none")
	}
	rootfsSize, err := parseSize("--rootfs-size", req.RootfsSize)
	if err != nil {
		return proto.PreparedRootfs{}, err
	}
	path, _, err := buildRootfs(ctx, data, artifact{req.RootfsTar, req.RootfsTarSHA}, req.Image, rootfsSize, logf)
	if err != nil {
		return proto.PreparedRootfs{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return proto.PreparedRootfs{}, err
	}
	return proto.PreparedRootfs{Name: filepath.Base(path), SizeBytes: info.Size()}, nil
}

// seedFSx writes a cached rootfs into a fresh volume as its disk.raw, sparse,
// so the volume can be snapshotted and cloned for every vm built from it.
func seedFSx(ctx context.Context, data string, seed proto.DiskSeed) error {
	if !rootfsCacheName.MatchString(seed.Rootfs) {
		return fmt.Errorf("%q is not the name of a built rootfs", seed.Rootfs)
	}
	src := filepath.Join(imagesDir(data), seed.Rootfs)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("the rootfs %s is not in this host's cache: %w", seed.Rootfs, err)
	}

	target, err := os.MkdirTemp(data, ".fsx-seed-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(target) }()
	if err := mountFSx(ctx, seed.FSx, target); err != nil {
		return err
	}
	if err := writeRawDisk(ctx, src, "raw", target); err != nil {
		_ = unmountFSx(context.WithoutCancel(ctx), target)
		return err
	}
	// The unmount flushes what the copy left in the page cache, so it decides
	// whether the seed made it to the volume.
	return unmountFSx(context.WithoutCancel(ctx), target)
}

func writeRawDisk(ctx context.Context, src, format, target string) error {
	tmp := filepath.Join(target, "."+fsxDiskRaw)
	out, err := exec.CommandContext(ctx, "qemu-img", "convert", "-q", "-f", format, "-O", "raw", "-S", "4k", src, tmp).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("qemu-img convert: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, filepath.Join(target, fsxDiskRaw))
}

var diskMoves sync.Map

func diskMoving(id string) bool {
	_, ok := diskMoves.Load(id)
	return ok
}

// moveDiskToFSx flattens a stopped vm's overlay and its backing file into
// disk.raw on an empty volume, then points the vm at it. The overlay is kept.
func moveDiskToFSx(ctx context.Context, data string, v vm, move proto.DiskMove) error {
	if v.FSx != nil {
		if *v.FSx == move.FSx {
			return nil
		}
		return fmt.Errorf("the disk of %s is already on %s:%s", v.Name, v.FSx.Server, v.FSx.Path)
	}
	if _, busy := diskMoves.LoadOrStore(v.ID, struct{}{}); busy {
		return fmt.Errorf("the disk of %s is already being moved", v.Name)
	}
	defer diskMoves.Delete(v.ID)
	if vmPID(data, v.ID) != 0 {
		return fmt.Errorf("%s is running; stop it before moving its disk", v.Name)
	}
	size, err := imageVirtualSize(ctx, v.Disk)
	if err != nil {
		return err
	}
	if size > move.SizeBytes {
		return fmt.Errorf("the disk is %d MiB, more than the %d MiB volume holds", size>>20, move.SizeBytes>>20)
	}

	moved := v
	moved.FSx = &move.FSx
	moved.Disk = fsxDiskPath(data, v.ID)
	moved.Backing = ""
	moved.LocalDisk, moved.LocalBacking = v.Disk, v.Backing
	target := fsxMountPoint(data, moved)
	if err := mountFSx(ctx, move.FSx, target); err != nil {
		return err
	}
	if err := writeRawDisk(ctx, v.Disk, "qcow2", target); err != nil {
		_ = unmountFSx(context.WithoutCancel(ctx), target)
		return err
	}
	if err := unmountFSx(context.WithoutCancel(ctx), target); err != nil {
		return err
	}
	return saveVM(data, moved)
}

func dropLocalDisk(data string, v vm) error {
	if v.LocalDisk == "" {
		return nil
	}
	if v.FSx == nil {
		return fmt.Errorf("%s still boots from its local disk", v.Name)
	}
	if filepath.Dir(v.LocalDisk) != vmDir(data, v.ID) {
		return fmt.Errorf("%s is not in the directory of %s", v.LocalDisk, v.Name)
	}
	if err := os.Remove(v.LocalDisk); err != nil && !os.IsNotExist(err) {
		return err
	}
	v.LocalDisk, v.LocalBacking = "", ""
	return saveVM(data, v)
}
