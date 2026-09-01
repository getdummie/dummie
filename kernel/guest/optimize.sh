#!/usr/bin/env sh

# virtio transports + devices: the only hardware a microvm guest ever sees
scripts/config --enable  CONFIG_VIRTIO
scripts/config --enable  CONFIG_VIRTIO_PCI
scripts/config --enable  CONFIG_VIRTIO_MMIO
scripts/config --enable  CONFIG_VIRTIO_MMIO_CMDLINE_DEVICES
scripts/config --enable  CONFIG_VIRTIO_BLK
scripts/config --enable  CONFIG_VIRTIO_NET
scripts/config --enable  CONFIG_HW_RANDOM_VIRTIO
scripts/config --enable  CONFIG_VIRTIO_CONSOLE
scripts/config --enable  CONFIG_VIRTIO_BALLOON

# rootfs: ext4 for writable disks, squashfs for the read-only image layers
scripts/config --enable  CONFIG_EXT4_FS
scripts/config --enable  CONFIG_BLOCK
scripts/config --enable  CONFIG_DEVTMPFS
scripts/config --enable  CONFIG_DEVTMPFS_MOUNT
scripts/config --enable  CONFIG_SQUASHFS
scripts/config --enable  CONFIG_SQUASHFS_ZLIB
scripts/config --enable  CONFIG_SQUASHFS_LZ4
scripts/config --enable  CONFIG_SQUASHFS_LZO
scripts/config --enable  CONFIG_SQUASHFS_XZ
scripts/config --enable  CONFIG_SQUASHFS_ZSTD
scripts/config --enable  CONFIG_SQUASHFS_XATTR       # container images carry security.* xattrs
scripts/config --enable  CONFIG_SQUASHFS_FILE_DIRECT
scripts/config --enable  CONFIG_SQUASHFS_DECOMP_MULTI_PERCPU
scripts/config --enable  CONFIG_BLK_DEV_LOOP         # needed only to mount a .squashfs file

# host-shared directories: virtiofs (preferred) with 9p as the fallback
scripts/config --enable  CONFIG_FUSE_FS
scripts/config --enable  CONFIG_VIRTIO_FS
scripts/config --enable  CONFIG_NET_9P
scripts/config --enable  CONFIG_NET_9P_VIRTIO
scripts/config --enable  CONFIG_9P_FS

# paravirt guest support: kvmclock, pv spinlocks and fast boot instead of emulated timers
scripts/config --enable  CONFIG_HYPERVISOR_GUEST
scripts/config --enable  CONFIG_PARAVIRT
scripts/config --enable  CONFIG_KVM_GUEST
scripts/config --enable  CONFIG_PARAVIRT_CLOCK
scripts/config --enable  CONFIG_PVH          # allows fast PVH direct-boot on the microvm machine
scripts/config --enable  CONFIG_X86_X2APIC

# serial console, wall clock and boot-time IP config from the kernel cmdline
scripts/config --enable  CONFIG_SERIAL_8250
scripts/config --enable  CONFIG_SERIAL_8250_CONSOLE
scripts/config --enable  CONFIG_SERIAL_8250
scripts/config --enable  CONFIG_SERIAL_8250_CONSOLE
scripts/config --enable  CONFIG_RTC_CLASS
scripts/config --enable  CONFIG_RTC_DRV_CMOS
scripts/config --enable  CONFIG_INET
scripts/config --enable  CONFIG_IP_PNP        # required for ip=10.68.0.2::... to work

# baseline syscalls and filesystems systemd refuses to boot without
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

# strip everything a headless vm cannot use, and compress with lz4 for fast boot
scripts/config --disable CONFIG_DRM
scripts/config --disable CONFIG_FB
scripts/config --disable CONFIG_VGA_CONSOLE
scripts/config --disable CONFIG_VT
scripts/config --disable CONFIG_SOUND
scripts/config --disable CONFIG_USB_SUPPORT
scripts/config --disable CONFIG_WLAN
scripts/config --disable CONFIG_CFG80211
scripts/config --disable CONFIG_BT
scripts/config --disable CONFIG_ATA
scripts/config --disable CONFIG_SCSI_LOWLEVEL
scripts/config --disable CONFIG_SUSPEND
scripts/config --disable CONFIG_HIBERNATION
scripts/config --enable  CONFIG_KERNEL_LZ4

# everything docker needs inside the guest: namespaces, cgroups, overlayfs and its netfilter rules
scripts/config --enable  CONFIG_NAMESPACES
scripts/config --enable  CONFIG_NET_NS
scripts/config --enable  CONFIG_PID_NS
scripts/config --enable  CONFIG_IPC_NS
scripts/config --enable  CONFIG_UTS_NS
scripts/config --enable  CONFIG_USER_NS        # rootless / userns-remap
scripts/config --enable  CONFIG_CGROUP_CPUACCT
scripts/config --enable  CONFIG_CGROUP_DEVICE
scripts/config --enable  CONFIG_CGROUP_FREEZER
scripts/config --enable  CONFIG_CGROUP_SCHED
scripts/config --enable  CONFIG_CGROUP_PIDS
scripts/config --enable  CONFIG_CPUSETS
scripts/config --enable  CONFIG_MEMCG
scripts/config --enable  CONFIG_BLK_CGROUP
scripts/config --enable  CONFIG_CFS_BANDWIDTH   # --cpus / cpu quota
scripts/config --enable  CONFIG_FAIR_GROUP_SCHED
scripts/config --enable  CONFIG_KEYS
scripts/config --enable  CONFIG_POSIX_MQUEUE
scripts/config --enable  CONFIG_OVERLAY_FS
scripts/config --enable  CONFIG_VETH
scripts/config --enable  CONFIG_BRIDGE
scripts/config --enable  CONFIG_BRIDGE_NETFILTER
scripts/config --enable  CONFIG_NF_CONNTRACK
scripts/config --enable  CONFIG_NF_NAT
scripts/config --enable  CONFIG_NF_TABLES        # iptables-nft backend
scripts/config --enable  CONFIG_IP_NF_FILTER
scripts/config --enable  CONFIG_IP_NF_NAT
scripts/config --enable  CONFIG_IP_NF_TARGET_MASQUERADE
scripts/config --enable  CONFIG_NETFILTER_XT_TARGET_MASQUERADE
scripts/config --enable  CONFIG_NETFILTER_XT_MATCH_ADDRTYPE
scripts/config --enable  CONFIG_NETFILTER_XT_MATCH_CONNTRACK
scripts/config --enable  CONFIG_NETFILTER_XT_MARK
scripts/config --enable  CONFIG_NF_TABLES_INET
scripts/config --enable  CONFIG_NF_TABLES_IPV4
scripts/config --enable  CONFIG_NF_TABLES_IPV6
scripts/config --enable  CONFIG_NFT_COMPAT      # lets iptables-nft use xt matches (addrtype, MASQUERADE)
scripts/config --enable  CONFIG_NFT_NAT         # the missing nat chain type — the actual failure
scripts/config --enable  CONFIG_NFT_MASQ
scripts/config --enable  CONFIG_NFT_REDIR
scripts/config --enable  CONFIG_NFT_CT          # docker rules match conntrack state
scripts/config --enable  CONFIG_BPF
scripts/config --enable  CONFIG_BPF_SYSCALL
scripts/config --enable  CONFIG_CGROUP_BPF      # the one that fixes BPF_CGROUP_DEVICE
scripts/config --enable  CONFIG_BPF_JIT         # optional, faster BPF
scripts/config --enable  CONFIG_VXLAN
scripts/config --enable  CONFIG_IP_VS

# tun for vpn clients in the guest, plus memory features that cut per-vm overhead
scripts/config --enable  CONFIG_TUN
scripts/config --enable  CONFIG_HUGETLBFS
scripts/config --enable  CONFIG_TRANSPARENT_HUGEPAGE
scripts/config --enable  CONFIG_TRANSPARENT_HUGEPAGE_MADVISE
scripts/config --enable  CONFIG_PARAVIRT_SPINLOCKS
scripts/config --enable  CONFIG_KSM

# suricata ips (nfqueue) and tc-based traffic shaping/mirroring
scripts/config --enable  CONFIG_NETFILTER_ADVANCED
scripts/config --enable  CONFIG_NETFILTER_NETLINK
scripts/config --enable  CONFIG_NETFILTER_NETLINK_QUEUE
scripts/config --enable  CONFIG_NETFILTER_NETLINK_QUEUE_CT      # optional, gives Suricata conntrack info
scripts/config --enable  CONFIG_NFT_QUEUE      # the `queue` verdict itself
scripts/config --enable  CONFIG_NFT_COUNTER    # counters on the drop rules
scripts/config --enable  CONFIG_IFB
scripts/config --enable  CONFIG_NET_SCH_HTB
scripts/config --enable  CONFIG_NET_SCH_INGRESS
scripts/config --enable  CONFIG_NET_CLS_U32
scripts/config --enable  CONFIG_NET_ACT_MIRRED
scripts/config --enable  CONFIG_IKCONFIG       # exposes the running config at /proc/config.gz
scripts/config --enable  CONFIG_IKCONFIG_PROC
