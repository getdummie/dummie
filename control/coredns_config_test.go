package main

import (
	"strings"
	"testing"

	"control/internal/db"
)

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
			if strings.Contains(out, "NXDOMAIN") {
				t.Errorf("a refusal was written as NXDOMAIN:\n%s", out)
			}
		})
	}
}

func TestGenerateCoreDNSConfigBindsNothing(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
	}, "1.1.1.1")

	if strings.Contains(out, "bind ") {
		t.Errorf("a server block binds an address; the gateway does not exist until a vm does:\n%s", out)
	}
}

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
	if strings.Count(out, "view vm_10_0_0_2_allow") != 1 ||
		strings.Count(out, "view vm_10_0_0_3_allow") != 1 {
		t.Errorf("view names are not one per vm:\n%s", out)
	}
}

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
