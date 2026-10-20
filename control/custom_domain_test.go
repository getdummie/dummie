package main

import (
	"net/netip"
	"testing"

	"github.com/miekg/dns"
)

func TestMatchFlattened(t *testing.T) {
	addrs := func(ss ...string) []netip.Addr {
		out := make([]netip.Addr, 0, len(ss))
		for _, s := range ss {
			out = append(out, netip.MustParseAddr(s))
		}
		return out
	}
	want := addrs("203.0.113.10", "2001:db8::10")

	cases := []struct {
		name string
		got  []netip.Addr
		ok   bool
	}{
		{"same addresses", addrs("203.0.113.10", "2001:db8::10"), true},
		{"only the v4 address", addrs("203.0.113.10"), true},
		{"v4 mapped into v6", addrs("::ffff:203.0.113.10"), true},
		{"another address", addrs("198.51.100.7"), false},
		{"a stray aaaa record", addrs("203.0.113.10", "2001:db8::99"), false},
	}
	for _, c := range cases {
		err := matchFlattened("example.com", "vm1.fleet.dev", c.got, want)
		if (err == nil) != c.ok {
			t.Errorf("%s: matchFlattened = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestMatchCNAMEChain(t *testing.T) {
	rr := func(s string) dns.RR {
		r, err := dns.NewRR(s)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cases := []struct {
		name    string
		answers []dns.RR
		ok      bool
	}{
		{"direct", []dns.RR{
			rr("www.example.com. 300 IN CNAME vm1.fleet.dev."),
			rr("vm1.fleet.dev. 300 IN A 203.0.113.10"),
		}, true},
		{"fleet name is itself a cname", []dns.RR{
			rr("www.example.com. 300 IN CNAME VM1.fleet.dev."),
			rr("vm1.fleet.dev. 300 IN CNAME fleet.dev."),
			rr("fleet.dev. 300 IN A 203.0.113.10"),
		}, true},
		{"straight at the apex", []dns.RR{
			rr("www.example.com. 300 IN CNAME fleet.dev."),
			rr("fleet.dev. 300 IN A 203.0.113.10"),
		}, false},
		{"another vm", []dns.RR{
			rr("www.example.com. 300 IN CNAME vm2.fleet.dev."),
			rr("vm2.fleet.dev. 300 IN CNAME fleet.dev."),
			rr("fleet.dev. 300 IN A 203.0.113.10"),
		}, false},
		{"no cname", []dns.RR{
			rr("www.example.com. 300 IN A 203.0.113.10"),
		}, false},
		{"an unrelated cname", []dns.RR{
			rr("other.example.com. 300 IN CNAME vm1.fleet.dev."),
			rr("www.example.com. 300 IN A 203.0.113.10"),
		}, false},
	}
	for _, c := range cases {
		chain := cnameChain("www.example.com", c.answers)
		err := matchCNAMEChain("www.example.com", "vm1.fleet.dev", chain)
		if (err == nil) != c.ok {
			t.Errorf("%s: matchCNAMEChain = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestSplitCustomDomainMarksTheApex(t *testing.T) {
	cases := map[string]string{
		"codingcoffee.dev":     "@",
		"www.codingcoffee.dev": "www",
		"example.co.uk":        "@",
		"a.b.example.co.uk":    "a.b",
	}
	for domain, want := range cases {
		if host, _ := splitCustomDomain(domain); host != want {
			t.Errorf("splitCustomDomain(%q) host = %q, want %q", domain, host, want)
		}
	}
}
