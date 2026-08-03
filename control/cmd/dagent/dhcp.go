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

// A guest on a /32 cannot install a default route on its own: the gateway is
// not inside its own subnet, so every conventional "address plus netmask plus
// router" configuration fails. DHCP option 121 (RFC 3442, classless static
// routes) is the way out -- it hands the client an explicit list of routes,
// including an on-link one for the gateway, so no inference is needed.
//
// That is why this exists rather than a kernel `ip=` command line: it needs no
// changes to the guest image beyond a stock DHCP client, and it works for
// images dagent did not build.
const (
  dhcpServerPort = 67
  dhcpClientPort = 68

  dhcpLeaseSeconds = 3600
  guestMTU         = 1500

  bootRequest = 1
  bootReply   = 2

  // Message types (option 53).
  dhcpDiscover = 1
  dhcpOffer    = 2
  dhcpRequest  = 3
  dhcpDecline  = 4
  dhcpAck      = 5
  dhcpNak      = 6

  // Options used here.
  optSubnetMask     = 1
  optRouter         = 3
  optDNS            = 6
  optMTU            = 26
  optRequestedIP    = 50
  optLeaseTime      = 51
  optMessageType    = 53
  optServerID       = 54
  optClasslessRoute = 121
  optEnd            = 255
)

var dhcpMagic = [4]byte{99, 130, 83, 99}

// dhcpServer answers only for VMs it can identify. There is no address pool and
// no lease database: the address is already decided by the allocator and
// recorded in the VM's state, and the MAC is derived from it. An unknown MAC
// gets no reply at all.
type dhcpServer struct {
  data string
  cfg  netConfig
}

// packet is a parsed BOOTP/DHCP message. Only the fields that matter here are
// kept; the fixed header is 236 bytes followed by the magic cookie and options.
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
    if code == 0 { // pad
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

// reply builds an OFFER or an ACK. yiaddr is what the client is being given;
// everything else describes how to use it.
func (s *dhcpServer) reply(req *packet, kind byte, yiaddr net.IP) []byte {
  b := make([]byte, 240, 400)
  b[0] = bootReply
  b[1] = 1 // ethernet
  b[2] = 6 // mac length
  binary.BigEndian.PutUint32(b[4:8], req.xid)
  binary.BigEndian.PutUint16(b[10:12], req.flags)
  copy(b[16:20], yiaddr.To4())                          // yiaddr
  copy(b[20:24], net.ParseIP(s.cfg.Gateway).To4())      // siaddr
  copy(b[24:28], req.giaddr.To4())                      // giaddr
  copy(b[28:44], req.chaddr)                            // chaddr
  copy(b[236:240], dhcpMagic[:])

  gw := net.ParseIP(s.cfg.Gateway).To4()
  add := func(code byte, data []byte) {
    b = append(b, code, byte(len(data)))
    b = append(b, data...)
  }

  add(optMessageType, []byte{kind})
  add(optServerID, gw)
  add(optLeaseTime, be32(dhcpLeaseSeconds))
  // A /32: the guest owns exactly one address and nothing else is on its wire.
  add(optSubnetMask, []byte{255, 255, 255, 255})
  add(optMTU, be16(guestMTU))

  // The important one. Two routes: the gateway itself, on-link on this
  // interface, and then everything else through it. Without the first, the
  // second is unusable -- which is the whole problem being solved here.
  routes := append(classlessRoute(gw, 32, net.IPv4zero.To4()),
    classlessRoute(net.IPv4zero.To4(), 0, gw)...)
  add(optClasslessRoute, routes)

  // RFC 3442 says a client that understands option 121 must ignore option 3.
  // Sent anyway for the ones that do not, where it is better than nothing.
  add(optRouter, gw)

  // The resolver is upstream, not on the gateway -- dagent runs none. A guest
  // still has to be allowed to reach it, so this address needs to be in the
  // VM's egress allowlist or the queue has to be accepting everything.
  if dns := net.ParseIP(s.cfg.DNS).To4(); dns != nil {
    add(optDNS, dns)
  }

  b = append(b, optEnd)
  return b
}

// classlessRoute encodes one route in option 121's compact form: a prefix
// length, only the significant octets of the destination, then the gateway.
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

// vmForMAC identifies the caller. The MAC is derived from the address at
// allocation time, so this is a lookup rather than a decision -- and a guest
// that lies about its MAC gets an address its tap will not accept, because the
// anti-spoof rule pins the pairing.
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

// serve runs the server until the context is cancelled.
//
// A raw socket is used rather than net.UDPConn because the reply has to be
// pinned to one interface: the client has no address yet, so the answer goes to
// the broadcast address, and without IP_PKTINFO the kernel would pick an
// interface by routing table rather than the tap the request arrived on.
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

  // Closing the socket is what unblocks the read below.
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
    return nil // renewals of a lease we always grant, declines, releases
  }

  v, ok := s.vmForMAC(req.chaddr)
  if !ok {
    // Not one of ours. Silence is the right answer: this socket sees every
    // broadcast on every interface the host has.
    return nil
  }
  yiaddr := net.ParseIP(v.Net.IP).To4()
  if yiaddr == nil {
    return fmt.Errorf("vm %s has an invalid address %q", v.ID, v.Net.IP)
  }

  kind := byte(dhcpOffer)
  if msgType == dhcpRequest {
    // A client asking for an address other than the one it was allocated is
    // told no, rather than being quietly given the right one.
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
  return sendBroadcast(fd, iface.Index, s.reply(req, kind, yiaddr))
}

// sendBroadcast writes one packet to 255.255.255.255:68 out of exactly one
// interface, chosen by index rather than by routing.
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
