package main

import (
  "encoding/binary"
  "fmt"
  "net"
  "os"
  "unsafe"

  "github.com/vishvananda/netlink"
  "golang.org/x/sys/unix"
)

// The network model is one point-to-point link per VM and nothing else:
//
//   guest 10.64.0.5/32  <--- tap-<id> --->  host 10.64.0.1/32
//
// Every VM is a /32. There is no shared segment, so there is no wire between
// two VMs for a packet to cross -- isolation is the absence of a path, and the
// nftables policy is the second layer over that, not the only one.
//
// The gateway address is the same on every tap. Linux is happy with one address
// on many interfaces, ARP is answered per-interface, and it means every guest
// gets an identical network configuration apart from its own address.
const (
  defaultPool    = "10.64.0.0/16"
  defaultGateway = "10.64.0.1"

  // tapPrefix is deliberately short: interface names are capped at 15 bytes
  // and the VM id takes 6 of them.
  tapPrefix = "dvm-"
)

// vmNet is the network half of a VM's desired state.
type vmNet struct {
  Tap     string `json:"tap"`
  IP      string `json:"ip"`      // the guest's /32
  Gateway string `json:"gateway"` // the host end, shared by the whole fleet
  MAC     string `json:"mac"`

  // Egress destinations this VM may reach, as CIDRs. Empty means the VM can
  // reach the host services and nothing else.
  Egress []string `json:"egress,omitempty"`

  RateMbit  int `json:"rate_mbit,omitempty"`
  BurstKbit int `json:"burst_kbit,omitempty"`
}

func (n *vmNet) tapName(id string) string { return tapPrefix + id }

// --- address allocation -----------------------------------------------------

// allocateIP picks the lowest free address in the pool. Allocation is derived
// from the VMs on disk rather than from a separate counter, so there is no
// second source of truth to drift.
func allocateIP(data, pool, gateway string) (string, error) {
  _, ipnet, err := net.ParseCIDR(pool)
  if err != nil {
    return "", fmt.Errorf("invalid pool %q: %w", pool, err)
  }
  gw := net.ParseIP(gateway)
  if gw == nil || !ipnet.Contains(gw) {
    return "", fmt.Errorf("gateway %q is not inside pool %s", gateway, pool)
  }

  taken := map[uint32]bool{ipToU32(gw): true}
  vms, err := listVMs(data)
  if err != nil {
    return "", err
  }
  for _, v := range vms {
    if v.Net == nil {
      continue
    }
    if ip := net.ParseIP(v.Net.IP); ip != nil {
      taken[ipToU32(ip)] = true
    }
  }

  first := ipToU32(ipnet.IP) + 1 // .0 is the network address
  ones, bits := ipnet.Mask.Size()
  last := ipToU32(ipnet.IP) + 1<<uint(bits-ones) - 2
  for n := first; n <= last; n++ {
    if !taken[n] {
      return u32ToIP(n).String(), nil
    }
  }
  return "", fmt.Errorf("no free addresses left in %s", pool)
}

// reserveIP validates an address the operator chose rather than allocating one.
//
// Pinning matters when the egress policy lives in Suricata rules: those rules
// name addresses, and an address handed out by lowest-free would silently move
// to a different VM the next time one is recreated -- so the rules would still
// apply, to the wrong machine.
func reserveIP(data, pool, gateway, want string) (string, error) {
  ip := net.ParseIP(want)
  if ip == nil || ip.To4() == nil {
    return "", fmt.Errorf("--ip %q is not an IPv4 address", want)
  }
  _, ipnet, err := net.ParseCIDR(pool)
  if err != nil {
    return "", fmt.Errorf("invalid pool %q: %w", pool, err)
  }
  if !ipnet.Contains(ip) {
    return "", fmt.Errorf("--ip %s is outside the pool %s", want, pool)
  }
  if ip.Equal(net.ParseIP(gateway)) {
    return "", fmt.Errorf("--ip %s is the gateway", want)
  }

  vms, err := listVMs(data)
  if err != nil {
    return "", err
  }
  for _, v := range vms {
    if v.Net != nil && v.Net.IP == ip.String() {
      return "", fmt.Errorf("--ip %s is already held by vm %s (%s)", want, v.ID, v.Name)
    }
  }
  return ip.String(), nil
}

func ipToU32(ip net.IP) uint32 {
  return binary.BigEndian.Uint32(ip.To4())
}

func u32ToIP(n uint32) net.IP {
  var b [4]byte
  binary.BigEndian.PutUint32(b[:], n)
  return net.IP(b[:])
}

// macForIP derives the guest's MAC from its address so it is stable across
// recreations and obvious in a packet capture. 52:54:00 is QEMU's OUI.
func macForIP(ip string) string {
  b := net.ParseIP(ip).To4()
  return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[1], b[2], b[3])
}

// --- tap devices ------------------------------------------------------------

// createTap opens a tap and returns both the interface and the file handle.
//
// The fd is the point: it is passed to QEMU directly, so the VM process never
// needs CAP_NET_ADMIN and cannot create, rename or reconfigure interfaces. The
// tap also has no persistence flag set, which means the kernel deletes it when
// the last fd closes -- a dead VM cannot leave a live interface behind.
func createTap(name string) (*os.File, error) {
  f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
  if err != nil {
    return nil, fmt.Errorf("could not open /dev/net/tun: %w", err)
  }

  var req struct {
    name  [unix.IFNAMSIZ]byte
    flags uint16
    _     [22]byte
  }
  if len(name) >= unix.IFNAMSIZ {
    f.Close()
    return nil, fmt.Errorf("tap name %q is too long", name)
  }
  copy(req.name[:], name)
  // IFF_NO_PI: no 4-byte packet-info header, which is what QEMU expects.
  // IFF_VNET_HDR is deliberately not set -- it buys offload performance at the
  // cost of a negotiation that has to match QEMU's vnet_hdr setting exactly.
  req.flags = unix.IFF_TAP | unix.IFF_NO_PI

  if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(),
    uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&req))); errno != 0 {
    f.Close()
    return nil, fmt.Errorf("TUNSETIFF %s: %w", name, errno)
  }
  return f, nil
}

// configureTap brings the link up and installs both halves of the point-to-point
// pair: the gateway address on the host end, and a host route to the guest's
// /32 out of this tap and no other.
func configureTap(name, guestIP, gateway string) error {
  link, err := netlink.LinkByName(name)
  if err != nil {
    return fmt.Errorf("tap %s vanished: %w", name, err)
  }

  // The same gateway address on every tap. It answers the guest's ARP on this
  // wire and gives host-originated traffic a deterministic source address.
  addr, err := netlink.ParseAddr(gateway + "/32")
  if err != nil {
    return err
  }
  if err := netlink.AddrAdd(link, addr); err != nil && !os.IsExist(err) {
    return fmt.Errorf("could not add %s to %s: %w", gateway, name, err)
  }

  if err := netlink.LinkSetUp(link); err != nil {
    return fmt.Errorf("could not bring %s up: %w", name, err)
  }

  dst := &net.IPNet{IP: net.ParseIP(guestIP).To4(), Mask: net.CIDRMask(32, 32)}
  route := &netlink.Route{
    LinkIndex: link.Attrs().Index,
    Dst:       dst,
    Scope:     netlink.SCOPE_LINK,
  }
  if err := netlink.RouteReplace(route); err != nil {
    return fmt.Errorf("could not route %s via %s: %w", guestIP, name, err)
  }
  return nil
}

// removeTap deletes the interface. Normally redundant -- the kernel reaps the
// tap when QEMU's fd closes -- but the reconciler needs it to clean up after a
// VM whose process died in a way that left the interface behind.
func removeTap(name string) error {
  link, err := netlink.LinkByName(name)
  if err != nil {
    return nil // already gone
  }
  return netlink.LinkDel(link)
}

// --- host plumbing ----------------------------------------------------------

// defaultUplink is the interface VM egress gets masqueraded out of: whichever
// one the host itself would use to reach the internet.
//
// This asks the kernel to route a packet rather than reading the main table,
// because a default route is not always there to be read -- policy rules and
// per-link tables are both common, and both are invisible to a plain listing.
func defaultUplink() (string, error) {
  probe := net.IPv4(1, 1, 1, 1)
  if routes, err := netlink.RouteGet(probe); err == nil {
    for _, r := range routes {
      if r.LinkIndex > 0 {
        link, err := netlink.LinkByIndex(r.LinkIndex)
        if err != nil {
          return "", err
        }
        return link.Attrs().Name, nil
      }
    }
  }

  // Fall back to an explicit default route, which gives a better message when
  // the host genuinely has no path out.
  routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
  if err != nil {
    return "", err
  }
  for _, r := range routes {
    if r.Dst == nil && r.LinkIndex > 0 {
      link, err := netlink.LinkByIndex(r.LinkIndex)
      if err != nil {
        return "", err
      }
      return link.Attrs().Name, nil
    }
  }
  return "", fmt.Errorf("could not work out which interface reaches the internet; pass --uplink")
}

// enableForwarding is required for any VM to reach anything beyond the host.
// Set here rather than assumed, because a host that reboots without it silently
// black-holes every guest.
func enableForwarding() error {
  const p = "/proc/sys/net/ipv4/ip_forward"
  b, err := os.ReadFile(p)
  if err != nil {
    return err
  }
  if len(b) > 0 && b[0] == '1' {
    return nil
  }
  return os.WriteFile(p, []byte("1\n"), 0o644)
}

// guestConfig is what the guest has to do with the address it was given. A /32
// cannot reach its own gateway until the on-link route exists, so this is not
// optional -- it is printed after create and served by the metadata service.
func guestConfig(n *vmNet) string {
  return fmt.Sprintf(`ip addr add %s/32 dev eth0
ip link set eth0 up
ip route add %s dev eth0 scope link
ip route add default via %s`, n.IP, n.Gateway, n.Gateway)
}
