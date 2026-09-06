package main

import (
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

const (
	nftTable = "dclient"

	setTaps   = "vm_taps"
	setVMIPs  = "vm_ips"
	setVMSrc  = "vm_src"
	setEgress = "egress_allow"

	metadataPort = 8345
)

type netConfig struct {
	Pool    string
	Gateway string
	Uplink  string

	DNS string

	Suricata bool
	Queues   uint16

	NoDockerCompat bool
}

func (c netConfig) resolver() string {
	if c.Suricata {
		return c.Gateway
	}
	return c.DNS
}

func applyBaseRuleset(cfg netConfig) error {
	c, err := nftables.New()
	if err != nil {
		return err
	}

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

	taps := &nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFIndex}
	vmIPs := &nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr}
	vmSrc := &nftables.Set{
		Table:         t,
		Name:          setVMSrc,
		KeyType:       nftables.MustConcatSetType(nftables.TypeIFIndex, nftables.TypeIPAddr),
		Concatenation: true,
	}
	egress := &nftables.Set{
		Table:         t,
		Name:          setEgress,
		KeyType:       nftables.MustConcatSetType(nftables.TypeIPAddr, nftables.TypeIPAddr),
		Concatenation: true,
		Interval:      true,
	}
	for _, s := range []*nftables.Set{taps, vmIPs, vmSrc, egress} {
		if err := c.AddSet(s, nil); err != nil {
			return fmt.Errorf("could not create set %s: %w", s.Name, err)
		}
	}

	drop := nftables.ChainPolicyDrop
	accept := nftables.ChainPolicyAccept

	fwd := c.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    t,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &drop,
	})

	c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID, Invert: true},
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{
			SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
			Xor:  binaryutil.NativeEndian.PutUint32(0),
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}})

	c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Payload{DestRegister: 9, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
		&expr.Lookup{SourceRegister: 1, SetName: setVMSrc, SetID: vmSrc.ID, Invert: true},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}})

	c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Meta{Key: expr.MetaKeyOIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}})

	c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Lookup{SourceRegister: 1, SetName: setVMIPs, SetID: vmIPs.ID},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}})

	allow := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
		&expr.Payload{DestRegister: 9, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Lookup{SourceRegister: 1, SetName: setEgress, SetID: egress.ID},
		&expr.Counter{},
	}
	if cfg.Suricata {
		allow = append(allow, &expr.Queue{Num: 0, Total: cfg.Queues, Flag: expr.QueueFlagFanout})
	} else {
		allow = append(allow, &expr.Verdict{Kind: expr.VerdictAccept})
	}
	c.AddRule(&nftables.Rule{Table: t, Chain: fwd, Exprs: allow})

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

	c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{
			SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
			Xor:  binaryutil.NativeEndian.PutUint32(0),
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}})

	c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_UDP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(dhcpServerPort)},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}})

	// Host services a guest may reach, by destination address. Everything but
	// intproxy answers on the gateway; intproxy has its own address because it
	// binds :443 specifically, which is how it coexists with dproxy's wildcard.
	for _, svc := range []struct {
		daddr []byte
		proto uint8
		port  uint16
	}{
		{gw, unix.IPPROTO_TCP, metadataPort},
		{gw, unix.IPPROTO_UDP, 53},
		{gw, unix.IPPROTO_TCP, 53},
		// 443 when the fleet has a wildcard certificate, 80 when it does not.
		// Which one intproxy binds is in its pushed config, not here.
		{net.ParseIP(intproxyAddr).To4(), unix.IPPROTO_TCP, 443},
		{net.ParseIP(intproxyAddr).To4(), unix.IPPROTO_TCP, 80},
	} {
		if svc.daddr == nil {
			continue
		}
		c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
			&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: svc.daddr},
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{svc.proto}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(svc.port)},
			&expr.Counter{},
			&expr.Verdict{Kind: expr.VerdictAccept},
		}})
	}

	c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_ICMP}},
		&expr.Verdict{Kind: expr.VerdictAccept},
	}})

	c.AddRule(&nftables.Rule{Table: t, Chain: in, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIF, Register: 1},
		&expr.Lookup{SourceRegister: 1, SetName: setTaps, SetID: taps.ID},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}})

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

func ifname(name string) []byte {
	b := make([]byte, 16)
	copy(b, name)
	return b
}

func tapIndex(tap string) ([]byte, error) {
	iface, err := net.InterfaceByName(tap)
	if err != nil {
		return nil, fmt.Errorf("could not resolve tap %s: %w", tap, err)
	}
	return binaryutil.NativeEndian.PutUint32(uint32(iface.Index)), nil
}

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
	idx, err := tapIndex(v.Net.Tap)
	if err != nil {
		return fmt.Errorf("vm %s: %w", v.ID, err)
	}

	if err := c.SetAddElements(&nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFIndex},
		[]nftables.SetElement{{Key: idx}}); err != nil {
		return err
	}
	if err := c.SetAddElements(&nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr},
		[]nftables.SetElement{{Key: ip}}); err != nil {
		return err
	}
	if err := c.SetAddElements(&nftables.Set{
		Table: t, Name: setVMSrc, Concatenation: true,
		KeyType: nftables.MustConcatSetType(nftables.TypeIFIndex, nftables.TypeIPAddr),
	}, []nftables.SetElement{{Key: srcKey(idx, ip)}}); err != nil {
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

	if idx, err := tapIndex(v.Net.Tap); err == nil {
		_ = c.SetDeleteElements(&nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFIndex},
			[]nftables.SetElement{{Key: idx}})
		_ = c.SetDeleteElements(&nftables.Set{
			Table: t, Name: setVMSrc, Concatenation: true,
			KeyType: nftables.MustConcatSetType(nftables.TypeIFIndex, nftables.TypeIPAddr),
		}, []nftables.SetElement{{Key: srcKey(idx, ip)}})
	}
	_ = c.SetDeleteElements(&nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr},
		[]nftables.SetElement{{Key: ip}})

	if elems, err := egressElements(v, ip); err == nil && len(elems) > 0 {
		_ = c.SetDeleteElements(&nftables.Set{
			Table: t, Name: setEgress, Concatenation: true, Interval: true,
			KeyType: nftables.MustConcatSetType(nftables.TypeIPAddr, nftables.TypeIPAddr),
		}, elems)
	}
	return c.Flush()
}

func srcKey(idx []byte, ip net.IP) []byte {
	return append(append([]byte{}, idx...), ip...)
}

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

func cidrRange(cidr string) (start, end []byte, err error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
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
