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

const (
	defaultPool    = "10.64.0.0/16"
	defaultGateway = "10.64.0.1"

	tapPrefix = "dvm-"
)

type vmNet struct {
	Tap     string `json:"tap"`
	IP      string `json:"ip"`
	Gateway string `json:"gateway"`
	DNS     string `json:"dns,omitempty"`
	MAC     string `json:"mac"`

	Egress []string `json:"egress,omitempty"`

	RateMbit  int `json:"rate_mbit,omitempty"`
	BurstKbit int `json:"burst_kbit,omitempty"`
}

func (n *vmNet) tapName(id string) string { return tapPrefix + id }

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
	if ip := net.ParseIP(intproxyAddr); ip != nil && ipnet.Contains(ip) {
		taken[ipToU32(ip)] = true
	}
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

	first := ipToU32(ipnet.IP) + 1
	ones, bits := ipnet.Mask.Size()
	last := ipToU32(ipnet.IP) + 1<<uint(bits-ones) - 2
	for n := first; n <= last; n++ {
		if !taken[n] {
			return u32ToIP(n).String(), nil
		}
	}
	return "", fmt.Errorf("no free addresses left in %s", pool)
}

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
	if ip.Equal(net.ParseIP(intproxyAddr)) {
		return "", fmt.Errorf("--ip %s is the integration proxy", want)
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

func macForIP(ip string) string {
	b := net.ParseIP(ip).To4()
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[1], b[2], b[3])
}

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
	req.flags = unix.IFF_TAP | unix.IFF_NO_PI

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(),
		uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&req))); errno != 0 {
		f.Close()
		return nil, fmt.Errorf("TUNSETIFF %s: %w", name, errno)
	}
	return f, nil
}

func configureTap(name, guestIP, gateway string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("tap %s vanished: %w", name, err)
	}

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

func removeTap(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil
	}
	return netlink.LinkDel(link)
}

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

func guestConfig(n *vmNet) string {
	return fmt.Sprintf(`ip addr add %s/32 dev eth0
ip link set eth0 up
ip route add %s dev eth0 scope link
ip route add default via %s`, n.IP, n.Gateway, n.Gateway)
}
