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

# 9. Docker / container runtime
#
# Everything below is what `docker`'s own check-config.sh flags as required.
# Built-in (not =m) to match the rest of this config and avoid needing modules.
#
# namespaces — the core of container isolation
scripts/config --enable  CONFIG_NAMESPACES
scripts/config --enable  CONFIG_NET_NS
scripts/config --enable  CONFIG_PID_NS
scripts/config --enable  CONFIG_IPC_NS
scripts/config --enable  CONFIG_UTS_NS
scripts/config --enable  CONFIG_USER_NS        # rootless / userns-remap
# cgroup controllers (bare CONFIG_CGROUPS is already on above)
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
# other core requirements
scripts/config --enable  CONFIG_KEYS
scripts/config --enable  CONFIG_POSIX_MQUEUE
# overlay2 storage driver
scripts/config --enable  CONFIG_OVERLAY_FS
# container networking: veth pair + bridge + netfilter/NAT for port publishing
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
# container nftables
scripts/config --enable CONFIG_NF_TABLES_INET
scripts/config --enable CONFIG_NF_TABLES_IPV4
scripts/config --enable CONFIG_NF_TABLES_IPV6
scripts/config --enable CONFIG_NFT_COMPAT      # lets iptables-nft use xt matches (addrtype, MASQUERADE)
scripts/config --enable CONFIG_NFT_NAT         # the missing nat chain type — the actual failure
scripts/config --enable CONFIG_NFT_MASQ
scripts/config --enable CONFIG_NFT_REDIR
scripts/config --enable CONFIG_NFT_CT          # docker rules match conntrack state
# bpf for runc v2
scripts/config --enable CONFIG_BPF
scripts/config --enable CONFIG_BPF_SYSCALL
scripts/config --enable CONFIG_CGROUP_BPF      # the one that fixes BPF_CGROUP_DEVICE
scripts/config --enable CONFIG_BPF_JIT         # optional, faster BPF
# overlay/swarm networks (drop these two if you never use overlay networking)
scripts/config --enable  CONFIG_VXLAN
scripts/config --enable  CONFIG_IP_VS

# 10. Running VMs *inside* this VM (L1 acting as a hypervisor)
#
# Section 4 only gives you /dev/kvm. Everything below is what QEMU / libvirt /
# firecracker / cloud-hypervisor additionally need to actually boot an L2 guest.
#
# NOTE: two things outside this script must also hold, or none of this matters:
#   - L0 must be loaded with `kvm_amd nested=1` (or `kvm_intel nested=1`)
#   - L1 must be started with `-cpu host` so svm/vmx is visible in CPUID
#
# tap devices — without this an L2 guest has no way to get a NIC at all
scripts/config --enable  CONFIG_TUN
# in-kernel virtio-net backend; without it every L2 packet round-trips
# through userspace QEMU, which nested networking really cannot afford
scripts/config --enable  CONFIG_VHOST
scripts/config --enable  CONFIG_VHOST_NET
# vsock — firecracker / cloud-hypervisor use it for their guest agent + API path
scripts/config --enable  CONFIG_VSOCKETS
scripts/config --enable  CONFIG_VHOST_VSOCK
# macvtap — alternative to bridge+tap when you want L2 straight on the L1 LAN
scripts/config --enable  CONFIG_MACVLAN
scripts/config --enable  CONFIG_MACVTAP
# huge pages for L2 memory — nested page-table walks are two-level, so this is
# the single biggest perf lever available (HUGETLBFS is already on by default)
scripts/config --enable  CONFIG_HUGETLBFS
scripts/config --enable  CONFIG_TRANSPARENT_HUGEPAGE
scripts/config --enable  CONFIG_TRANSPARENT_HUGEPAGE_MADVISE
# L1 is itself a guest — pv spinlocks avoid burning vCPU time spinning while
# the L0 scheduler has the lock holder descheduled
scripts/config --enable  CONFIG_PARAVIRT_SPINLOCKS
# page dedup across similar L2 guests; drop if you only run one or two VMs
scripts/config --enable  CONFIG_KSM

# 10. For running suricata with nftables
scripts/config --enable  CONFIG_NETFILTER_ADVANCED
scripts/config --enable  CONFIG_NETFILTER_NETLINK
scripts/config --enable  CONFIG_NETFILTER_NETLINK_QUEUE
scripts/config --enable  CONFIG_NETFILTER_NETLINK_QUEUE_CT      # optional, gives Suricata conntrack info
# nftables expressions dagent emits — separate symbols from the core table support
scripts/config --enable  CONFIG_NFT_QUEUE      # the `queue` verdict itself
scripts/config --enable  CONFIG_NFT_COUNTER    # counters on the drop rules
# per-VM bandwidth: HTB on the tap, ingress redirected to an ifb
scripts/config --enable  CONFIG_IFB
scripts/config --enable  CONFIG_NET_SCH_HTB
scripts/config --enable  CONFIG_NET_SCH_INGRESS
scripts/config --enable  CONFIG_NET_CLS_U32
scripts/config --enable  CONFIG_NET_ACT_MIRRED
# so /proc/config.gz exists and you can answer this question in one command
scripts/config --enable  CONFIG_IKCONFIG
scripts/config --enable  CONFIG_IKCONFIG_PROC
