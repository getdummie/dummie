package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	dhcpServerPort = 67
	dhcpClientPort = 68

	dhcpLeaseSeconds = 3600
	guestMTU         = 1500

	bootRequest = 1
	bootReply   = 2

	dhcpDiscover = 1
	dhcpOffer    = 2
	dhcpRequest  = 3
	dhcpDecline  = 4
	dhcpAck      = 5
	dhcpNak      = 6

	optSubnetMask     = 1
	optRouter         = 3
	optDNS            = 6
	optHostname       = 12
	optMTU            = 26
	optRequestedIP    = 50
	optLeaseTime      = 51
	optMessageType    = 53
	optServerID       = 54
	optClasslessRoute = 121
	optEnd            = 255
)

var dhcpMagic = [4]byte{99, 130, 83, 99}

type dhcpServer struct {
	data string
	cfg  netConfig
}

type packet struct {
	op      byte
	xid     uint32
	flags   uint16
	giaddr  net.IP
	chaddr  net.HardwareAddr
	options map[byte][]byte
}

func parsePacket(b []byte) (*packet, error) {
	if len(b) < 240 {
		return nil, fmt.Errorf("short packet (%d bytes)", len(b))
	}
	if [4]byte(b[236:240]) != dhcpMagic {
		return nil, fmt.Errorf("not a dhcp packet")
	}
	p := &packet{
		op:      b[0],
		xid:     binary.BigEndian.Uint32(b[4:8]),
		flags:   binary.BigEndian.Uint16(b[10:12]),
		giaddr:  net.IP(b[24:28]),
		chaddr:  net.HardwareAddr(b[28 : 28+int(b[2])]),
		options: map[byte][]byte{},
	}

	for i := 240; i < len(b); {
		code := b[i]
		if code == optEnd {
			break
		}
		if code == 0 {
			i++
			continue
		}
		if i+1 >= len(b) {
			break
		}
		length := int(b[i+1])
		if i+2+length > len(b) {
			break
		}
		p.options[code] = b[i+2 : i+2+length]
		i += 2 + length
	}
	return p, nil
}

func (s *dhcpServer) reply(req *packet, kind byte, yiaddr net.IP, name string) []byte {
	b := make([]byte, 240, 400)
	b[0] = bootReply
	b[1] = 1
	b[2] = 6
	binary.BigEndian.PutUint32(b[4:8], req.xid)
	binary.BigEndian.PutUint16(b[10:12], req.flags)
	copy(b[16:20], yiaddr.To4())
	copy(b[20:24], net.ParseIP(s.cfg.Gateway).To4())
	copy(b[24:28], req.giaddr.To4())
	copy(b[28:44], req.chaddr)
	copy(b[236:240], dhcpMagic[:])

	gw := net.ParseIP(s.cfg.Gateway).To4()
	add := func(code byte, data []byte) {
		b = append(b, code, byte(len(data)))
		b = append(b, data...)
	}

	add(optMessageType, []byte{kind})
	add(optServerID, gw)
	add(optLeaseTime, be32(dhcpLeaseSeconds))
	add(optSubnetMask, []byte{255, 255, 255, 255})
	add(optMTU, be16(guestMTU))

	if h := guestHostname(name); h != "" {
		add(optHostname, []byte(h))
	}

	routes := append(classlessRoute(gw, 32, net.IPv4zero.To4()),
		classlessRoute(net.IPv4zero.To4(), 0, gw)...)
	add(optClasslessRoute, routes)

	add(optRouter, gw)

	if dns := net.ParseIP(s.cfg.resolver()).To4(); dns != nil {
		add(optDNS, dns)
	}

	b = append(b, optEnd)
	return b
}

func classlessRoute(dst net.IP, prefix int, via net.IP) []byte {
	significant := (prefix + 7) / 8
	out := make([]byte, 0, 1+significant+4)
	out = append(out, byte(prefix))
	out = append(out, dst.To4()[:significant]...)
	return append(out, via.To4()...)
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func be16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func (s *dhcpServer) vmForMAC(mac net.HardwareAddr) (vm, bool) {
	vms, err := listVMs(s.data)
	if err != nil {
		return vm{}, false
	}
	for _, v := range vms {
		if v.Net != nil && v.Net.MAC == mac.String() {
			return v, true
		}
	}
	return vm{}, false
}

func (s *dhcpServer) serve(ctx context.Context) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return fmt.Errorf("could not open a dhcp socket: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		unix.Close(fd)
		return err
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BROADCAST, 1); err != nil {
		unix.Close(fd)
		return err
	}
	if err := unix.Bind(fd, &unix.SockaddrInet4{Port: dhcpServerPort}); err != nil {
		unix.Close(fd)
		return fmt.Errorf("could not bind udp/%d: %w", dhcpServerPort, err)
	}

	go func() {
		<-ctx.Done()
		_ = unix.Close(fd)
	}()

	log.Printf("dhcp server on udp/%d", dhcpServerPort)
	buf := make([]byte, 1500)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("dhcp read failed: %w", err)
		}
		if err := s.handle(fd, buf[:n]); err != nil {
			log.Printf("dhcp: %v", err)
		}
	}
}

func (s *dhcpServer) handle(fd int, raw []byte) error {
	req, err := parsePacket(raw)
	if err != nil {
		return err
	}
	if req.op != bootRequest {
		return nil
	}
	msgType := byte(0)
	if v, ok := req.options[optMessageType]; ok && len(v) == 1 {
		msgType = v[0]
	}
	if msgType != dhcpDiscover && msgType != dhcpRequest {
		return nil
	}

	v, ok := s.vmForMAC(req.chaddr)
	if !ok {
		return nil
	}
	yiaddr := net.ParseIP(v.Net.IP).To4()
	if yiaddr == nil {
		return fmt.Errorf("vm %s has an invalid address %q", v.ID, v.Net.IP)
	}

	kind := byte(dhcpOffer)
	if msgType == dhcpRequest {
		if want, present := req.options[optRequestedIP]; present && !net.IP(want).Equal(yiaddr) {
			kind = dhcpNak
		} else {
			kind = dhcpAck
		}
	}

	iface, err := net.InterfaceByName(v.Net.Tap)
	if err != nil {
		return fmt.Errorf("vm %s: tap %s: %w", v.ID, v.Net.Tap, err)
	}
	return sendBroadcast(fd, iface.Index, s.reply(req, kind, yiaddr, v.Name))
}

func sendBroadcast(fd, ifindex int, payload []byte) error {
	info := unix.Inet4Pktinfo{Ifindex: int32(ifindex)}
	oob := make([]byte, unix.CmsgSpace(unix.SizeofInet4Pktinfo))
	h := (*unix.Cmsghdr)(unsafe.Pointer(&oob[0]))
	h.Level = unix.IPPROTO_IP
	h.Type = unix.IP_PKTINFO
	h.SetLen(unix.CmsgLen(unix.SizeofInet4Pktinfo))
	copy(oob[unix.CmsgLen(0):], (*[unix.SizeofInet4Pktinfo]byte)(unsafe.Pointer(&info))[:])

	return unix.Sendmsg(fd, payload, oob, &unix.SockaddrInet4{
		Port: dhcpClientPort,
		Addr: [4]byte{255, 255, 255, 255},
	}, 0)
}
