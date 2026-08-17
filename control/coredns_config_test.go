package main

import (
	"strings"
	"testing"

	"control/internal/db"
)

// Whatever else it contains, the file has to end in a block that refuses. Every
// guest with no allowances falls through to it, so an empty or absent one is a
// host that resolves the whole internet for them.
func TestGenerateCoreDNSConfigAlwaysRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []db.ListVMNetworkTargetsByClientRow
	}{
		{"no vms", nil},
		{"one vm with nothing", []db.ListVMNetworkTargetsByClientRow{noTargets("10.0.0.2", "alpha")}},
		{"one vm with a domain", []db.ListVMNetworkTargetsByClientRow{
			row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := generateCoreDNSConfig(tc.rows, "1.1.1.1")
			if !strings.Contains(out, corednsRefuseAll) {
				t.Errorf("the fleet-wide refusal is missing:\n%s", out)
			}
			// NXDOMAIN would assert the name does not exist, which is a lie a stub
			// resolver caches and a user goes looking for a typo over.
			if strings.Contains(out, "NXDOMAIN") {
				t.Errorf("a refusal was written as NXDOMAIN:\n%s", out)
			}
		})
	}
}

// No block binds an address. The gateway only exists on a tap, so a bind pins the
// resolver to an address that is absent on a host with no VMs -- coredns exits at
// startup and the reconciler restarts it forever. Isolation does not depend on the
// bind: it is the per-VM views, which match on source address.
func TestGenerateCoreDNSConfigBindsNothing(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
	}, "1.1.1.1")

	if strings.Contains(out, "bind ") {
		t.Errorf("a server block binds an address; the gateway does not exist until a vm does:\n%s", out)
	}
}

// A VM gets a zone for each name it was granted and a view pinning that zone to
// its address. The view is the whole isolation story in this file: without it one
// guest's allowance answers every guest's lookup.
func TestGenerateCoreDNSConfigScopesToTheVM(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
		row("10.0.0.3", "beta", "domain", "other.com", "", ""),
	}, "9.9.9.9")

	for _, want := range []string{
		"example.com:53 {",
		"expr client_ip() == '10.0.0.2'",
		"other.com:53 {",
		"expr client_ip() == '10.0.0.3'",
		"forward . 9.9.9.9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// Views share one namespace across the file, so two VMs must not land on one
	// name -- the second would silently inherit the first's policy.
	if strings.Count(out, "view vm_10_0_0_2_allow") != 1 ||
		strings.Count(out, "view vm_10_0_0_3_allow") != 1 {
		t.Errorf("view names are not one per vm:\n%s", out)
	}
}

// A VM with no domain allowances gets no block, and specifically must not get an
// empty allow block: a zone list that is empty would turn `example.com:53 {` into
// `:53 {`, which is a syntax error at best and a catch-all that forwards at worst.
func TestGenerateCoreDNSConfigWritesNoBlockForAVMWithNoDomains(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		noTargets("10.0.0.2", "alpha"),
		row("10.0.0.3", "beta", "ip", "1.2.3.4", "tcp", "22"),
	}, "1.1.1.1")

	if strings.Contains(out, "forward .") {
		t.Errorf("a forwarder was written for a host whose vms may resolve nothing:\n%s", out)
	}
	if strings.Contains(out, "1.2.3.4") {
		t.Errorf("an address allowance leaked into the corefile:\n%s", out)
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("vm %s is not accounted for in the file at all:\n%s", want, out)
		}
	}
}

// Lookup-only is the case the two generated files disagree about on purpose: the
// resolver answers the name and the ruleset grants nothing. It is what makes an
// allowance like ssh to a hostname work, so the name has to be in this file even
// though it is in no pass rule.
func TestGenerateCoreDNSConfigAnswersLookupOnlyDomains(t *testing.T) {
	rows := []db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "github.com", "", domainPortsNone),
	}
	out := generateCoreDNSConfig(rows, "1.1.1.1")
	if !strings.Contains(out, "github.com:53 {") {
		t.Errorf("a lookup-only domain is not resolvable:\n%s", out)
	}
	if rules := generateSuricataRules(rows); strings.Contains(rules, "pass") {
		t.Errorf("a lookup-only domain generated a pass rule:\n%s", rules)
	}
}

// One VM, several names: they belong in one block, deduplicated, in a stable
// order. Two blocks for the same view and zone would be a conflict coredns
// resolves by ignoring one of them.
func TestGenerateCoreDNSConfigGroupsAndSortsZones(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", "443"),
		row("10.0.0.2", "alpha", "domain", "example.com", "", "80"),
		row("10.0.0.2", "alpha", "domain", "aaa.com", "", ""),
	}, "1.1.1.1")

	if !strings.Contains(out, "aaa.com:53 example.com:53 {") {
		t.Errorf("zones are not deduplicated and sorted into one block:\n%s", out)
	}
}
