package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"

	"control/internal/proto"
)

var vmIDPattern = regexp.MustCompile(`^[0-9a-f]{6}$`)

// loadMovableVM returns ok=false, with no error, for a vm this host does not have.
func loadMovableVM(data, id string) (vm, bool, error) {
	if !vmIDPattern.MatchString(id) {
		return vm{}, false, fmt.Errorf("%q is not a vm id", id)
	}
	v, err := loadVM(data, id)
	if errors.Is(err, fs.ErrNotExist) {
		return vm{}, false, nil
	}
	if err != nil {
		return vm{}, false, err
	}
	switch {
	case v.FSx == nil:
		return v, true, fmt.Errorf("the disk of %s is on this host, not on fsx", v.Name)
	case diskMoving(v.ID):
		return v, true, fmt.Errorf("the disk of %s is being moved", v.Name)
	case vmPID(data, v.ID) != 0:
		return v, true, fmt.Errorf("%s is running; stop it first", v.Name)
	}
	return v, true, nil
}

func exportVM(data, id string) (*proto.VMRecord, error) {
	v, ok, err := loadMovableVM(data, id)
	if err != nil || !ok {
		return nil, err
	}
	if v.Boot != bootDirect || v.Initrd != "" {
		return nil, fmt.Errorf("only a %s-boot vm with no initrd can change host", bootDirect)
	}
	rec := &proto.VMRecord{
		ID:        v.ID,
		Name:      v.Name,
		CreatedAt: v.CreatedAt,
		Boot:      string(v.Boot),
		CPUs:      v.CPUs,
		MemoryMiB: v.MemoryMiB,
		Append:    v.Append,
		GuestInit: v.GuestInit,
		DiskBytes: v.DiskBytes,
		FSx:       v.FSx,
	}
	if v.Net != nil {
		rec.Network = true
		rec.IP = v.Net.IP
		rec.Egress = v.Net.Egress
		rec.RateMbit, rec.BurstKbit = v.Net.RateMbit, v.Net.BurstKbit
	}
	return rec, nil
}

// attachVM records a stopped vm around a disk already on fsx. Nothing is
// mounted or started; the next start does both, as for any stopped vm.
func attachVM(ctx context.Context, data string, rec proto.VMRecord) (vm, error) {
	if !vmIDPattern.MatchString(rec.ID) {
		return vm{}, fmt.Errorf("%q is not a vm id", rec.ID)
	}
	if rec.FSx == nil {
		return vm{}, errors.New("only a vm whose disk is on fsx can be attached")
	}
	if existing, err := loadVM(data, rec.ID); err == nil {
		if existing.FSx != nil && *existing.FSx == *rec.FSx {
			return existing, nil
		}
		return vm{}, fmt.Errorf("vm id %s is already taken on this host", rec.ID)
	}
	if bootMode(rec.Boot) != bootDirect || rec.Kernel == "" {
		return vm{}, fmt.Errorf("only a %s-boot vm with a kernel can be attached", bootDirect)
	}
	if rec.CPUs < 1 || rec.MemoryMiB < 64 {
		return vm{}, errors.New("a vm needs at least 1 cpu and 64 MiB of memory")
	}

	v := vm{
		ID:        rec.ID,
		Name:      rec.Name,
		CreatedAt: rec.CreatedAt,
		Boot:      bootDirect,
		CPUs:      rec.CPUs,
		MemoryMiB: rec.MemoryMiB,
		Append:    rec.Append,
		GuestInit: rec.GuestInit,
		Disk:      fsxDiskPath(data, rec.ID),
		FSx:       rec.FSx,
		DiskBytes: rec.DiskBytes,
	}
	var err error
	if v.Kernel, err = (artifact{ref: rec.Kernel}).resolve(ctx, imagesDir(data)); err != nil {
		return vm{}, err
	}
	if os.Geteuid() == 0 {
		if v.UID, err = allocateUID(data); err != nil {
			return vm{}, err
		}
		if err := ensureTraversable(data); err != nil {
			return vm{}, err
		}
	}
	if rec.Network {
		cfg, err := loadNetConfig(data)
		if err != nil {
			return vm{}, err
		}
		ip, err := allocateIP(data, cfg.Pool, cfg.Gateway)
		if err != nil {
			return vm{}, err
		}
		v.Net = &vmNet{
			IP:        ip,
			Gateway:   cfg.Gateway,
			DNS:       cfg.resolver(),
			MAC:       macForIP(ip),
			Egress:    rec.Egress,
			RateMbit:  rec.RateMbit,
			BurstKbit: rec.BurstKbit,
		}
		v.Net.Tap = v.Net.tapName(v.ID)
	}
	if err := os.MkdirAll(vmPath(data, v.ID, vmRunDir), 0o700); err != nil {
		return vm{}, err
	}
	if err := saveVM(data, v); err != nil {
		_ = os.RemoveAll(vmDir(data, v.ID))
		return vm{}, err
	}
	return v, nil
}

// releaseVM forgets a stopped fsx vm, leaving its volume alone. A local disk it
// was moved off goes with it.
func releaseVM(ctx context.Context, data, id string) error {
	v, ok, err := loadMovableVM(data, id)
	if err != nil || !ok {
		return err
	}
	return removeVM(ctx, data, v)
}
