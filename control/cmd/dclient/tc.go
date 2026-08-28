package main

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const ifbDevice = "dclient-ifb"

const htbQuantum = 1500

func ensureIFB() (netlink.Link, error) {
	if link, err := netlink.LinkByName(ifbDevice); err == nil {
		return link, netlink.LinkSetUp(link)
	}
	ifb := &netlink.Ifb{LinkAttrs: netlink.LinkAttrs{Name: ifbDevice}}
	if err := netlink.LinkAdd(ifb); err != nil {
		return nil, fmt.Errorf("could not create %s (is the ifb module available?): %w", ifbDevice, err)
	}
	link, err := netlink.LinkByName(ifbDevice)
	if err != nil {
		return nil, err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, err
	}
	root := netlink.NewHtb(netlink.QdiscAttrs{
		LinkIndex: link.Attrs().Index,
		Handle:    netlink.MakeHandle(1, 0),
		Parent:    netlink.HANDLE_ROOT,
	})
	if err := netlink.QdiscAdd(root); err != nil {
		return nil, fmt.Errorf("could not add the root qdisc to %s: %w", ifbDevice, err)
	}
	return link, nil
}

func classID(ip string) uint16 {
	b := net.ParseIP(ip).To4()
	return uint16(b[2])<<8 | uint16(b[3])
}

func applyBandwidth(n *vmNet) error {
	if n.RateMbit <= 0 {
		return nil
	}
	rate := uint64(n.RateMbit) * 1000 * 1000 / 8
	burst := uint32(n.BurstKbit) * 1000 / 8
	if burst == 0 {
		burst = uint32(rate / 10)
	}
	if burst < htbQuantum {
		burst = htbQuantum
	}

	tap, err := netlink.LinkByName(n.Tap)
	if err != nil {
		return err
	}

	root := netlink.NewHtb(netlink.QdiscAttrs{
		LinkIndex: tap.Attrs().Index,
		Handle:    netlink.MakeHandle(1, 0),
		Parent:    netlink.HANDLE_ROOT,
	})
	if err := netlink.QdiscAdd(root); err != nil {
		return fmt.Errorf("could not add the root qdisc to %s: %w", n.Tap, err)
	}
	class := netlink.NewHtbClass(netlink.ClassAttrs{
		LinkIndex: tap.Attrs().Index,
		Handle:    netlink.MakeHandle(1, 1),
		Parent:    netlink.MakeHandle(1, 0),
	}, netlink.HtbClassAttrs{
		Rate:    rate,
		Ceil:    rate,
		Buffer:  burst,
		Cbuffer: burst,
		Quantum: htbQuantum,
	})
	if err := netlink.ClassAdd(class); err != nil {
		return fmt.Errorf("could not shape %s: %w", n.Tap, err)
	}

	ifb, err := ensureIFB()
	if err != nil {
		return err
	}
	ingress := &netlink.Ingress{QdiscAttrs: netlink.QdiscAttrs{
		LinkIndex: tap.Attrs().Index,
		Handle:    netlink.MakeHandle(0xffff, 0),
		Parent:    netlink.HANDLE_INGRESS,
	}}
	if err := netlink.QdiscAdd(ingress); err != nil {
		return fmt.Errorf("could not add the ingress qdisc to %s: %w", n.Tap, err)
	}
	redirect := &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: tap.Attrs().Index,
			Parent:    netlink.MakeHandle(0xffff, 0),
			Priority:  1,
			Protocol:  unix.ETH_P_ALL,
		},
		Actions: []netlink.Action{netlink.NewMirredAction(ifb.Attrs().Index)},
	}
	if err := netlink.FilterAdd(redirect); err != nil {
		return fmt.Errorf("could not redirect %s to %s: %w", n.Tap, ifbDevice, err)
	}

	minor := classID(n.IP)
	ifbClass := netlink.NewHtbClass(netlink.ClassAttrs{
		LinkIndex: ifb.Attrs().Index,
		Handle:    netlink.MakeHandle(1, minor),
		Parent:    netlink.MakeHandle(1, 0),
	}, netlink.HtbClassAttrs{
		Rate:    rate,
		Ceil:    rate,
		Buffer:  burst,
		Cbuffer: burst,
		Quantum: htbQuantum,
	})
	if err := netlink.ClassAdd(ifbClass); err != nil {
		return fmt.Errorf("could not shape %s on %s: %w", n.IP, ifbDevice, err)
	}

	ip := net.ParseIP(n.IP).To4()
	sel := &netlink.U32{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: ifb.Attrs().Index,
			Parent:    netlink.MakeHandle(1, 0),
			Priority:  1,
			Protocol:  unix.ETH_P_IP,
		},
		Sel: &netlink.TcU32Sel{
			Nkeys: 1,
			Flags: netlink.TC_U32_TERMINAL,
			Keys: []netlink.TcU32Key{{
				Mask: 0xffffffff,
				Val:  uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3]),
				Off:  12,
			}},
		},
		ClassId: netlink.MakeHandle(1, minor),
	}
	if err := netlink.FilterAdd(sel); err != nil {
		return fmt.Errorf("could not classify %s on %s: %w", n.IP, ifbDevice, err)
	}
	return nil
}

func removeBandwidth(n *vmNet) error {
	if n == nil || n.RateMbit <= 0 {
		return nil
	}
	ifb, err := netlink.LinkByName(ifbDevice)
	if err != nil {
		return nil
	}
	minor := classID(n.IP)
	class := netlink.NewHtbClass(netlink.ClassAttrs{
		LinkIndex: ifb.Attrs().Index,
		Handle:    netlink.MakeHandle(1, minor),
		Parent:    netlink.MakeHandle(1, 0),
	}, netlink.HtbClassAttrs{})
	if err := netlink.ClassDel(class); err != nil {
		return fmt.Errorf("could not remove the class for %s: %w", n.IP, err)
	}
	return nil
}
