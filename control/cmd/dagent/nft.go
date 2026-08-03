package main

import (
  "fmt"
  "net"

  "github.com/google/nftables"
  "github.com/google/nftables/binaryutil"
  "github.com/google/nftables/expr"
  "golang.org/x/sys/unix"
)

// Everything dagent enforces lives in one table, `inet dagent`, which is
// replaced atomically at startup. Per-VM state is held entirely in named sets:
// creating or destroying a VM adds or removes set *elements* and never touches
// a rule. That is what makes the policy safe against dagent itself -- the rules
// are already in the kernel, they fail closed, and a crashed agent cannot open
// a hole because opening one requires a rule it never writes.
const (
  nftTable = "dagent"

  setTaps   = "vm_taps"      // ifname
  setVMIPs  = "vm_ips"       // ipv4_addr
  setVMSrc  = "vm_src"       // ifname . ipv4_addr  (the only legal pairing)
  setEgress = "egress_allow" // ipv4_addr . ipv4_addr-range (per-VM allowlist)

  // metadataPort is where the per-VM identity service listens on the gateway.
  metadataPort = 80
)

// netConfig is the host-wide network policy: everything that is the same for
// every VM.
type netConfig struct {
  Pool    string
  Gateway string
  Uplink  string

  // DNS is handed to guests over DHCP. It points outside the fleet because
  // dagent runs no resolver: the gateway would answer nothing.
  DNS string

  Suricata bool   // when set, VM egress is queued to Suricata before acceptance
  Queues   uint16 // must match the number of -q flags Suricata is started with
}

// applyBaseRuleset installs the whole table in one atomic transaction, deleting
// any previous version first. Replacing rather than patching means the kernel
// state after this call is exactly what this code says it is, with no residue
// from an older build of the agent.
//
// The per-VM sets come back empty; the reconciler repopulates them from the
// desired state on disk immediately afterwards.
func applyBaseRuleset(cfg netConfig) error {
  c, err := nftables.New()
  if err != nil {
    return err
  }

  // Delete-then-add in the same batch: if anything below fails, the old table
  // survives untouched because nothing was committed. The delete is conditional
  // because removing a table that was never there fails the whole transaction.
  existing, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
  if err != nil {
    return fmt.Errorf("could not read the current ruleset: %w", err)
  }
  for _, t := range existing {
    if t.Name == nftTable {
      c.DelTable(t)
    }
  }
  t := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: nftTable})

  taps := &nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFName}
  vmIPs := &nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr}
  vmSrc := &nftables.Set{
    Table:         t,
    Name:          setVMSrc,
    KeyType:       nftables.MustConcatSetType(nftables.TypeIFName, nftables.TypeIPAddr),
    Concatenation: true,
  }
  egress := &nftables.Set{
    Table:         t,
    Name:          setEgress,
    KeyType:       nftables.MustConcatSetType(nftables.TypeIPAddr, nftables.TypeIPAddr),
    Concatenation: true,
    Interval:      true, // the destination half is a CIDR range
  }
  for _, s := range []*nftables.Set{taps, vmIPs, vmSrc, egress} {
    if err := c.AddSet(s, nil); err != nil {
      return fmt.Errorf("could not create set %s: %w", s.Name, err)
    }
  }

  drop := nftables.ChainPolicyDrop
  accept := nftables.ChainPolicyAccept

  // --- forward: everything between a VM and the outside world ---------------
  fwd := c.AddChain(&nftables.Chain{
    Name:     "forward",
    Table:    t,
    Type:     nftables.ChainTypeFilter,
    Hooknum:  nftables.ChainHookForward,
    Priority: nftables.ChainPriorityFilter,
    Policy:   &drop, // default deny, per VM, by construction
  })

  // Return traffic for flows we already allowed.
  c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
    &expr.Ct{Register: 1, Key: expr.CtKeySTATE},
    &expr.Bitwise{
      SourceRegister: 1, DestRegister: 1, Len: 4,
      Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
      Xor:  binaryutil.NativeEndian.PutUint32(0),
    },
    &expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
    &expr.Verdict{Kind: expr.VerdictAccept},
  }})

  // Anti-spoof: a packet from a tap may only carry the one address that tap was
  // issued. Pinned as a pair, so a VM cannot borrow another VM's address either.
  c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
    &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
    &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
    &expr.Payload{DestRegister: 2, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
    &expr.Lookup{SourceRegister: 1, SetName: setVMSrc, SetID: vmSrc.ID, Invert: true},
    &expr.Counter{},
    &expr.Verdict{Kind: expr.VerdictDrop},
  }})

  // No VM-to-VM traffic, ever -- by output interface...
  c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
    &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
    &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
    &expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
    &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
    &expr.Counter{},
    &expr.Verdict{Kind: expr.VerdictDrop},
  }})

  // ...and again by destination address, which closes the hairpin even if the
  // packet somehow leaves by an interface that is not a tap.
  c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
    &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
    &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
    &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
    &expr.Lookup{SourceRegister: 1, SetName: setVMIPs, SetID: vmIPs.ID},
    &expr.Counter{},
    &expr.Verdict{Kind: expr.VerdictDrop},
  }})

  // The allowlist: source VM paired with a permitted destination range.
  allow := []expr.Any{
    &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
    &expr.Payload{DestRegister: 9, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
    &expr.Lookup{SourceRegister: 1, SetName: setEgress, SetID: egress.ID},
    &expr.Counter{},
  }
  if cfg.Suricata {
    // Queued in the forward hook, which runs before NAT, so Suricata sees the
    // real per-VM source address rather than the host's post-masquerade one.
    // Bypass on failure would fail open, so it is deliberately not set.
    allow = append(allow, &expr.Queue{Num: 0, Total: cfg.Queues, Flag: expr.QueueFlagFanout})
  } else {
    allow = append(allow, &expr.Verdict{Kind: expr.VerdictAccept})
  }
  c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: allow})

  // --- input: what a VM may ask of the host itself --------------------------
  // Policy accept, because this chain also sees the host's ordinary traffic and
  // must not lock the operator out; VM traffic is dropped explicitly at the end.
  in := c.AddChain(&nftables.Chain{
    Name:     "input",
    Table:    t,
    Type:     nftables.ChainTypeFilter,
    Hooknum:  nftables.ChainHookInput,
    Priority: nftables.ChainPriorityFilter,
    Policy:   &accept,
  })

  gw := net.ParseIP(cfg.Gateway).To4()
  if gw == nil {
    return fmt.Errorf("invalid gateway %q", cfg.Gateway)
  }

  // DHCP, which needs its own rule: a client with no address yet broadcasts to
  // 255.255.255.255, so it never matches a destination of the gateway.
  c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
    &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
    &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
    &expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
    &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_UDP}},
    &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
    &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(dhcpServerPort)},
    &expr.Verdict{Kind: expr.VerdictAccept},
  }})

  // Metadata service and, later, the filtering resolver.
  for _, svc := range []struct {
    proto uint8
    port  uint16
  }{
    {unix.IPPROTO_TCP, metadataPort},
    {unix.IPPROTO_UDP, 53},
    {unix.IPPROTO_TCP, 53},
  } {
    c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
      &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
      &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
      &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
      &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: gw},
      &expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
      &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{svc.proto}},
      &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
      &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(svc.port)},
      &expr.Verdict{Kind: expr.VerdictAccept},
    }})
  }

  // Ping the gateway, purely so an operator can tell "no route" apart from
  // "policy denied" from inside a guest.
  c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
    &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
    &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
    &expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
    &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_ICMP}},
    &expr.Verdict{Kind: expr.VerdictAccept},
  }})

  // Everything else a VM sends at the host.
  c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
    &expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
    &expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
    &expr.Counter{},
    &expr.Verdict{Kind: expr.VerdictDrop},
  }})

  // --- nat: egress leaves as the host ---------------------------------------
  post := c.AddChain(&nftables.Chain{
    Name:     "postrouting",
    Table:    t,
    Type:     nftables.ChainTypeNAT,
    Hooknum:  nftables.ChainHookPostrouting,
    Priority: nftables.ChainPriorityNATSource,
    Policy:   &accept,
  })
  c.AddRule(&nftables.Rule{Table: t, Chain: post, Exprs: []expr.Any{
    &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
    &expr.Lookup{SourceRegister: 1, SetName: setVMIPs, SetID: vmIPs.ID},
    &expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
    &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(cfg.Uplink)},
    &expr.Masq{},
  }})

  if err := c.Flush(); err != nil {
    return fmt.Errorf("could not install the nftables ruleset: %w", err)
  }
  return nil
}

// ifname pads an interface name to the fixed 16-byte field the kernel compares.
func ifname(name string) []byte {
  b := make([]byte, 16)
  copy(b, name)
  return b
}

// --- per-VM elements --------------------------------------------------------

// addVMPolicy admits one VM to the policy. This runs before QEMU starts: a VM
// must never exist without its rules, and the ordering is what guarantees it.
func addVMPolicy(v vm) error {
  c, err := nftables.New()
  if err != nil {
    return err
  }
  t := &nftables.Table{Family: nftables.TableFamilyINet, Name: nftTable}
  ip := net.ParseIP(v.Net.IP).To4()
  if ip == nil {
    return fmt.Errorf("vm %s has an invalid address %q", v.ID, v.Net.IP)
  }

  if err := c.SetAddElements(&nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFName},
    []nftables.SetElement{{Key: ifname(v.Net.Tap)}}); err != nil {
    return err
  }
  if err := c.SetAddElements(&nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr},
    []nftables.SetElement{{Key: ip}}); err != nil {
    return err
  }
  if err := c.SetAddElements(&nftables.Set{
    Table: t, Name: setVMSrc, Concatenation: true,
    KeyType: nftables.MustConcatSetType(nftables.TypeIFName, nftables.TypeIPAddr),
  }, []nftables.SetElement{{Key: append(ifname(v.Net.Tap), ip...)}}); err != nil {
    return err
  }

  if elems, err := egressElements(v, ip); err != nil {
    return err
  } else if len(elems) > 0 {
    if err := c.SetAddElements(&nftables.Set{
      Table: t, Name: setEgress, Concatenation: true, Interval: true,
      KeyType: nftables.MustConcatSetType(nftables.TypeIPAddr, nftables.TypeIPAddr),
    }, elems); err != nil {
      return err
    }
  }
  return c.Flush()
}

// removeVMPolicy is the exact reverse, and runs after the VM is gone.
func removeVMPolicy(v vm) error {
  if v.Net == nil {
    return nil
  }
  c, err := nftables.New()
  if err != nil {
    return err
  }
  t := &nftables.Table{Family: nftables.TableFamilyINet, Name: nftTable}
  ip := net.ParseIP(v.Net.IP).To4()
  if ip == nil {
    return nil
  }

  _ = c.SetDeleteElements(&nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFName},
    []nftables.SetElement{{Key: ifname(v.Net.Tap)}})
  _ = c.SetDeleteElements(&nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr},
    []nftables.SetElement{{Key: ip}})
  _ = c.SetDeleteElements(&nftables.Set{
    Table: t, Name: setVMSrc, Concatenation: true,
    KeyType: nftables.MustConcatSetType(nftables.TypeIFName, nftables.TypeIPAddr),
  }, []nftables.SetElement{{Key: append(ifname(v.Net.Tap), ip...)}})

  if elems, err := egressElements(v, ip); err == nil && len(elems) > 0 {
    _ = c.SetDeleteElements(&nftables.Set{
      Table: t, Name: setEgress, Concatenation: true, Interval: true,
      KeyType: nftables.MustConcatSetType(nftables.TypeIPAddr, nftables.TypeIPAddr),
    }, elems)
  }
  return c.Flush()
}

// egressElements turns this VM's allowlist into (source, destination-range)
// pairs. The source half pins the entry to one VM, which is what makes the
// allowlist per-VM rather than fleet-wide.
func egressElements(v vm, ip net.IP) ([]nftables.SetElement, error) {
  var elems []nftables.SetElement
  for _, cidr := range v.Net.Egress {
    start, end, err := cidrRange(cidr)
    if err != nil {
      return nil, fmt.Errorf("vm %s: %w", v.ID, err)
    }
    elems = append(elems, nftables.SetElement{
      Key:    append(append([]byte{}, ip...), start...),
      KeyEnd: append(append([]byte{}, ip...), end...),
    })
  }
  return elems, nil
}

// cidrRange returns the inclusive first and last address of a CIDR, which is
// how an interval set stores it.
func cidrRange(cidr string) (start, end []byte, err error) {
  ip, ipnet, err := net.ParseCIDR(cidr)
  if err != nil {
    // A bare address is a perfectly reasonable thing to allow.
    if single := net.ParseIP(cidr); single != nil && single.To4() != nil {
      b := single.To4()
      return b, b, nil
    }
    return nil, nil, fmt.Errorf("%q is not an address or CIDR", cidr)
  }
  if ip.To4() == nil {
    return nil, nil, fmt.Errorf("%q is not IPv4", cidr)
  }
  first := ipnet.IP.To4()
  last := make(net.IP, 4)
  for i := 0; i < 4; i++ {
    last[i] = first[i] | ^ipnet.Mask[i]
  }
  return first, last, nil
}
