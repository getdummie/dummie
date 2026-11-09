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
	"syscall"

	"control/internal/proto"
)

const (
	vmFSxMount = "disk"
	fsxDiskRaw = "disk.raw"

	fsxMountOptions = "nfsvers=4.1,hard,nconnect=16,timeo=600,retrans=2,noresvport"
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
	if err := writeSeed(ctx, src, target); err != nil {
		_ = unmountFSx(context.WithoutCancel(ctx), target)
		return err
	}
	// The unmount flushes what the copy left in the page cache, so it decides
	// whether the seed made it to the volume.
	return unmountFSx(context.WithoutCancel(ctx), target)
}

func writeSeed(ctx context.Context, src, target string) error {
	tmp := filepath.Join(target, "."+fsxDiskRaw)
	out, err := exec.CommandContext(ctx, "qemu-img", "convert", "-q", "-f", "raw", "-O", "raw", "-S", "4k", src, tmp).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("qemu-img convert: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, filepath.Join(target, fsxDiskRaw))
}
