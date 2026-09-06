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
			out := generateCoreDNSConfig(tc.rows, "1.1.1.1", "example.test")
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
	}, "1.1.1.1", "example.test")

	if strings.Contains(out, "bind ") {
		t.Errorf("a server block binds an address; the gateway does not exist until a vm does:\n%s", out)
	}
}

func TestGenerateCoreDNSConfigScopesToTheVM(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
		row("10.0.0.3", "beta", "domain", "other.com", "", ""),
	}, "9.9.9.9", "example.test")

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
	}, "1.1.1.1", "example.test")

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
	out := generateCoreDNSConfig(rows, "1.1.1.1", "example.test")
	if !strings.Contains(out, "github.com:53 {") {
		t.Errorf("a lookup-only domain is not resolvable:\n%s", out)
	}
	if rules := generateSuricataRules(rows); strings.Contains(rules, "pass") {
		t.Errorf("a lookup-only domain generated a pass rule:\n%s", rules)
	}
}

func TestGenerateCoreDNSConfigForwardsEveryNameWhenEverythingIsAllowed(t *testing.T) {
	rows := []db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "ip", targetEverywhere, "any", ""),
		row("10.0.0.3", "beta", "domain", "example.com", "", ""),
	}
	out := generateCoreDNSConfig(rows, "1.1.1.1", "example.test")

	if !strings.Contains(out, ".:53 {\n    view vm_10_0_0_2_allow {") {
		t.Errorf("the open vm did not get a forwarder for the root zone:\n%s", out)
	}
	if strings.Contains(out, "vm_10_0_0_2_deny") {
		t.Errorf("the open vm still has a refusal view:\n%s", out)
	}
	if !strings.Contains(out, "example.com:53 {") {
		t.Errorf("another vm's allowlist was disturbed:\n%s", out)
	}
	if !strings.Contains(out, corednsRefuseAll) {
		t.Errorf("the fleet-wide refusal is missing:\n%s", out)
	}
}

func TestGenerateSuricataRulesPassesEverythingWhenEverythingIsAllowed(t *testing.T) {
	out := generateSuricataRules([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "ip", targetEverywhere, "any", ""),
	})

	if !strings.Contains(out, "pass ip 10.0.0.2 any -> "+targetEverywhere+" any") {
		t.Errorf("no blanket pass rule was written:\n%s", out)
	}
	if strings.Contains(out, "no name-based allowances") {
		t.Errorf("the open vm still got its own blanket deny:\n%s", out)
	}
}

func TestGenerateCoreDNSConfigKeepsTheAllowlistForANarrowerCIDR(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "ip", targetEverywhere, "tcp", "443"),
	}, "1.1.1.1", "example.test")

	if strings.Contains(out, "forward .") {
		t.Errorf("everything on one port opened the resolver:\n%s", out)
	}
}

func TestGenerateCoreDNSConfigAnswersTheIntegrationZone(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		noTargets("10.0.0.2", "alpha"),
	}, "1.1.1.1", "example.test")

	for _, want := range []string{
		"int.example.test:53 {",
		"template IN A int.example.test {",
		`answer "{{ .Name }} 60 IN A ` + intproxyAddr + `"`,
		"template IN AAAA int.example.test {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// A guest with no allowances at all must still resolve the integration proxy:
// resolving the name grants nothing, because intproxy asks the control server
// whether the vm is attached before it forwards anything.
func TestGenerateCoreDNSConfigIntegrationZoneIsNotViewGated(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		noTargets("10.0.0.2", "alpha"),
	}, "1.1.1.1", "example.test")

	zone := strings.Index(out, "int.example.test:53 {")
	end := strings.Index(out[zone:], "\n}\n")
	if zone < 0 || end < 0 {
		t.Fatalf("the integration zone is missing:\n%s", out)
	}
	if strings.Contains(out[zone:zone+end], "view ") {
		t.Errorf("the integration zone carries a view, so a vm with no allowances cannot resolve it:\n%s", out)
	}
	if refuse := strings.Index(out, corednsRefuseAll); refuse >= 0 && refuse < zone {
		t.Errorf("the refuse-everything block precedes the integration zone:\n%s", out)
	}
}

func TestGenerateCoreDNSConfigOmitsTheIntegrationZoneWithoutATLD(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		noTargets("10.0.0.2", "alpha"),
	}, "1.1.1.1", "")

	if strings.Contains(out, "int.:53") || strings.Contains(out, intproxyAddr) {
		t.Errorf("a fleet with no domain got an integration zone:\n%s", out)
	}
}

func TestGenerateCoreDNSConfigGroupsAndSortsZones(t *testing.T) {
	out := generateCoreDNSConfig([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", "443"),
		row("10.0.0.2", "alpha", "domain", "example.com", "", "80"),
		row("10.0.0.2", "alpha", "domain", "aaa.com", "", ""),
	}, "1.1.1.1", "example.test")

	if !strings.Contains(out, "aaa.com:53 example.com:53 {") {
		t.Errorf("zones are not deduplicated and sorted into one block:\n%s", out)
	}
}
