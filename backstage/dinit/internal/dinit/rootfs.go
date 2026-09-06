package dinit

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// dclient sizes the per-vm qcow2 overlay to whatever disk size was asked for, but
// the ext4 inside it was built once, sized to the image tar. Nothing else grows it.
const (
	ext4SuperMagic = 0xEF53

	// _IOW('f', 16, __u64): resize2fs' online path, run against the mount itself.
	ext4IocResizeFS = 0x40086610

	sectorSize = 512
)

// growRootfs expands the root filesystem to fill its block device.
func growRootfs() {
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil {
		log.Printf("could not stat the root filesystem: %v", err)
		return
	}
	if st.Type != ext4SuperMagic || st.Bsize <= 0 {
		log.Printf("not growing the root filesystem: type %#x, block size %d", st.Type, st.Bsize)
		return
	}

	var sb unix.Stat_t
	if err := unix.Stat("/", &sb); err != nil {
		log.Printf("could not stat /: %v", err)
		return
	}
	dev := uint64(sb.Dev)
	size, err := blockDeviceSize(unix.Major(dev), unix.Minor(dev))
	if err != nil {
		log.Printf("could not size the root device: %v", err)
		return
	}

	blocks := size / uint64(st.Bsize)
	if blocks <= st.Blocks {
		log.Printf("the root filesystem already fills its device (%d blocks of %d bytes)", st.Blocks, st.Bsize)
		return
	}

	f, err := os.Open("/")
	if err != nil {
		log.Printf("could not open / to resize it: %v", err)
		return
	}
	defer f.Close()

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), ext4IocResizeFS,
		uintptr(unsafe.Pointer(&blocks))); errno != 0 {
		log.Printf("could not grow the root filesystem to %d blocks: %v", blocks, errno)
		return
	}
	log.Printf("grew the root filesystem from %d to %d blocks of %d bytes", st.Blocks, blocks, st.Bsize)
}

func blockDeviceSize(major, minor uint32) (uint64, error) {
	return readSectorCount(fmt.Sprintf("/sys/dev/block/%d:%d/size", major, minor))
}

func readSectorCount(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	sectors, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return sectors * sectorSize, nil
}
