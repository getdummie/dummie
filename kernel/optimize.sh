#!/usr/bin/env sh

# 1. Virtio — both transports + all the devices you use

scripts/config --enable  CONFIG_VIRTIO
scripts/config --enable  CONFIG_VIRTIO_PCI
scripts/config --enable  CONFIG_VIRTIO_MMIO
scripts/config --enable  CONFIG_VIRTIO_MMIO_CMDLINE_DEVICES
scripts/config --enable  CONFIG_VIRTIO_BLK
scripts/config --enable  CONFIG_VIRTIO_NET
scripts/config --enable  CONFIG_HW_RANDOM_VIRTIO
scripts/config --enable  CONFIG_VIRTIO_CONSOLE
scripts/config --enable  CONFIG_VIRTIO_BALLOON

# 2. Root filesystem + block

scripts/config --enable  CONFIG_EXT4_FS
scripts/config --enable  CONFIG_BLOCK
scripts/config --enable  CONFIG_DEVTMPFS
scripts/config --enable  CONFIG_DEVTMPFS_MOUNT

# 3. Directory sharing (virtiofs + 9p — both, so you can pick later)

scripts/config --enable  CONFIG_FUSE_FS
scripts/config --enable  CONFIG_VIRTIO_FS
scripts/config --enable  CONFIG_NET_9P
scripts/config --enable  CONFIG_NET_9P_VIRTIO
scripts/config --enable  CONFIG_9P_FS

# 4. Nested virtualization (the whole point — AMD)

scripts/config --enable  CONFIG_VIRTUALIZATION
scripts/config --enable  CONFIG_KVM
scripts/config --enable  CONFIG_KVM_AMD
scripts/config --enable CONFIG_KVM_INTEL

# 5. KVM-guest paravirt — big boot-time win (kvmclock, no TSC calibration stalls)

scripts/config --enable  CONFIG_HYPERVISOR_GUEST
scripts/config --enable  CONFIG_PARAVIRT
scripts/config --enable  CONFIG_KVM_GUEST
scripts/config --enable  CONFIG_PARAVIRT_CLOCK
scripts/config --enable  CONFIG_PVH          # allows fast PVH direct-boot on the microvm machine
scripts/config --enable  CONFIG_X86_X2APIC

# 6. Console + kernel-level networking (your ip= cmdline needs IP_PNP)

scripts/config --enable  CONFIG_SERIAL_8250
scripts/config --enable  CONFIG_SERIAL_8250_CONSOLE
scripts/config --enable  CONFIG_SERIAL_8250
scripts/config --enable  CONFIG_SERIAL_8250_CONSOLE
scripts/config --enable  CONFIG_RTC_CLASS
scripts/config --enable  CONFIG_RTC_DRV_CMOS
scripts/config --enable  CONFIG_INET
scripts/config --enable  CONFIG_IP_PNP        # required for ip=10.68.0.2::... to work

# 7. systemd prerequisites (defconfig usually has these, set explicitly to be safe)

scripts/config --enable  CONFIG_CGROUPS
scripts/config --enable  CONFIG_INOTIFY_USER
scripts/config --enable  CONFIG_SIGNALFD
scripts/config --enable  CONFIG_TIMERFD
scripts/config --enable  CONFIG_EPOLL
scripts/config --enable  CONFIG_FHANDLE
scripts/config --enable  CONFIG_TMPFS
scripts/config --enable  CONFIG_PROC_FS
scripts/config --enable  CONFIG_SYSFS
scripts/config --enable  CONFIG_SECCOMP        # systemd sandboxing; optional but expected

# 8. Strip everything a microvm never has — smaller image, less probing, faster boot
#
# graphics / console clutter (you're serial + ssh only)
scripts/config --disable CONFIG_DRM
scripts/config --disable CONFIG_FB
scripts/config --disable CONFIG_VGA_CONSOLE
scripts/config --disable CONFIG_VT
scripts/config --disable CONFIG_SOUND
# no USB / wireless / bluetooth on a microvm  (CFG80211 was your regulatory.db boot stall earlier)
scripts/config --disable CONFIG_USB_SUPPORT
scripts/config --disable CONFIG_WLAN
scripts/config --disable CONFIG_CFG80211
scripts/config --disable CONFIG_BT
# no real storage controllers — virtio-blk only
scripts/config --disable CONFIG_ATA
scripts/config --disable CONFIG_SCSI_LOWLEVEL
# no power management in a VM
scripts/config --disable CONFIG_SUSPEND
scripts/config --disable CONFIG_HIBERNATION
# faster decompression than gzip
scripts/config --enable  CONFIG_KERNEL_LZ4
