package main

import (
	"strings"
	"testing"

	"control/internal/db"
)

func row(ip, name, kind, dest, transport, ports string) db.ListVMNetworkTargetsByClientRow {
	return db.ListVMNetworkTargetsByClientRow{
		VMIP: ip, VMName: name, HostVMID: "vm-" + name,
		Kind: kind, Destination: dest, Transport: transport, Ports: ports,
	}
}

func noTargets(ip, name string) db.ListVMNetworkTargetsByClientRow {
	return db.ListVMNetworkTargetsByClientRow{VMIP: ip, VMName: name, HostVMID: "vm-" + name}
}

func TestGenerateSuricataRulesAlwaysDenies(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []db.ListVMNetworkTargetsByClientRow
	}{
		{"no targets", nil},
		{"some targets", []db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "a", "domain", "example.com", "", "")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := generateSuricataRules(tc.rows)
			for _, want := range []string{
				"drop ip $HOME_NET any -> any any",
				"drop tcp $HOME_NET any -> any ![80,443]",
				"drop tls $HOME_NET any -> any any",
				"ssl_state:client_hello;",
				"drop http $HOME_NET any -> any any",
				"drop tcp $HOME_NET any -> any [80,443]",
				"app-layer-protocol:!http; app-layer-protocol:!tls;",
				"dsize:>0;",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("generated ruleset is missing the deny %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestGenerateSuricataRulesScopesToTheVM(t *testing.T) {
	out := generateSuricataRules([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
		row("10.0.0.3", "beta", "ip", "1.2.3.4", "tcp", "443"),
	})

	for _, want := range []string{
		`pass tls 10.0.0.2 any -> any 443`,
		`pass http 10.0.0.2 any -> any 80`,
		`pass tcp 10.0.0.3 any -> 1.2.3.4 443`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pass tls $HOME_NET") || strings.Contains(out, "pass tcp $HOME_NET") {
		t.Errorf("a pass rule was written against the whole pool:\n%s", out)
	}
}

func TestGenerateSuricataRulesNeverPassesDNS(t *testing.T) {
	out := generateSuricataRules([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
	})
	if strings.Contains(out, "pass dns") {
		t.Errorf("a pass dns rule was generated:\n%s", out)
	}
}

func TestGenerateSuricataRulesBlanketDeniesWithoutANameRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []db.ListVMNetworkTargetsByClientRow
		want bool
	}{
		{"no targets at all", []db.ListVMNetworkTargetsByClientRow{noTargets("10.0.0.2", "alpha")}, true},
		{"addresses only",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "ip", "1.2.3.4", "tcp", "22")}, true},
		{"lookup-only domain",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "domain", "example.com", "", "none")}, true},
		{"a domain on both web ports",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "domain", "example.com", "", "")}, false},
		{"a domain on one web port",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "domain", "example.com", "", "443")}, false},
		{"addresses and a domain", []db.ListVMNetworkTargetsByClientRow{
			row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
			row("10.0.0.2", "alpha", "ip", "1.2.3.4", "tcp", "22"),
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := generateSuricataRules(tc.rows)
			got := strings.Contains(out, "drop ip 10.0.0.2 any -> any any")
			if got != tc.want {
				t.Errorf("blanket deny present = %v, want %v:\n%s", got, tc.want, out)
			}
		})
	}
}

func TestGenerateSuricataRulesCoversEveryVMOnTheHost(t *testing.T) {
	out := generateSuricataRules([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
		noTargets("10.0.0.3", "beta"),
	})
	if !strings.Contains(out, "10.0.0.3") {
		t.Errorf("the vm with no allowances is not in the ruleset at all:\n%s", out)
	}
	if !strings.Contains(out, "nothing allowed for beta") {
		t.Errorf("the vm with no allowances is not described as such:\n%s", out)
	}
}

func TestGenerateSuricataRulesSidsAreUnique(t *testing.T) {
	out := generateSuricataRules([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
		row("10.0.0.2", "alpha", "domain", "other.com", "", ""),
		row("10.0.0.3", "beta", "ip", "1.2.3.4", "any", "80,443"),
		row("10.0.0.3", "beta", "ip", "10.0.0.0/8", "any", ""),
		noTargets("10.0.0.4", "gamma"),
		noTargets("10.0.0.5", "delta"),
	})

	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "sid:")
		if i < 0 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		sid := line[i+len("sid:") : i+len("sid:")+strings.Index(line[i+len("sid:"):], ";")]
		if seen[sid] {
			t.Errorf("sid %s appears twice:\n%s", sid, out)
		}
		seen[sid] = true
	}
}

func TestTargetRulesTransportAndPorts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   db.ListVMNetworkTargetsByClientRow
		wants []string
	}{
		{"any transport, no port",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "any", ""),
			[]string{`pass ip 10.0.0.2 any -> 1.2.3.4 any`}},
		{"any transport with ports",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "any", "8080"),
			[]string{`pass tcp 10.0.0.2 any -> 1.2.3.4 8080`, `pass udp 10.0.0.2 any -> 1.2.3.4 8080`}},
		{"port list is bracketed",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "tcp", "80,443"),
			[]string{`pass tcp 10.0.0.2 any -> 1.2.3.4 [80,443]`}},
		{"port range is not",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "udp", "1000:2000"),
			[]string{`pass udp 10.0.0.2 any -> 1.2.3.4 1000:2000`}},
		{"icmp",
			row("10.0.0.2", "a", "ip", "8.8.8.8", "icmp", ""),
			[]string{`pass icmp 10.0.0.2 any -> 8.8.8.8 any`}},
		{"domain matches subdomains only",
			row("10.0.0.2", "a", "domain", "example.com", "", ""),
			[]string{`tls.sni; dotprefix; content:".example.com"; nocase; endswith;`,
				`http.host; dotprefix; content:".example.com"; endswith;`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sid := suricataPassSidBase
			got := strings.Join(targetRules(tc.row, &sid), "\n")
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestTargetRulesICMPDoesNotWiden(t *testing.T) {
	sid := suricataPassSidBase
	got := strings.Join(targetRules(row("10.0.0.2", "a", "ip", "8.8.8.8", "icmp", ""), &sid), "\n")
	if strings.Contains(got, "pass ip ") || strings.Contains(got, "pass tcp ") || strings.Contains(got, "pass udp ") {
		t.Errorf("an icmp allowance compiled to something wider:\n%s", got)
	}
}

func TestTargetRulesDomainPorts(t *testing.T) {
	for _, tc := range []struct {
		ports string
		want  []string
	}{
		{"", []string{`pass tls 10.0.0.2 any -> any 443`, `pass http 10.0.0.2 any -> any 80`}},
		{"443", []string{`pass tls 10.0.0.2 any -> any 443`}},
		{"80", []string{`pass http 10.0.0.2 any -> any 80`}},
		{"none", nil},
	} {
		t.Run("ports "+tc.ports, func(t *testing.T) {
			sid := suricataPassSidBase
			got := targetRules(row("10.0.0.2", "a", "domain", "example.com", "", tc.ports), &sid)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d rules, want %d:\n%s", len(got), len(tc.want), strings.Join(got, "\n"))
			}
			for i, want := range tc.want {
				if !strings.Contains(got[i], want) {
					t.Errorf("rule %d is %q, want it to contain %q", i, got[i], want)
				}
			}
			if want := suricataPassSidBase + len(tc.want); sid != want {
				t.Errorf("sid counter is at %d after %d rules, want %d", sid, len(got), want)
			}
		})
	}
}

func TestRuleTextCannotBreakOutOfAnOption(t *testing.T) {
	r := row("10.0.0.2", "a", "ip", "1.2.3.4", "tcp", "443")
	r.Note = `bad"; drop ip any any -> any any (sid:1;)`

	sid := suricataPassSidBase
	got := strings.Join(targetRules(r, &sid), "\n")

	if n := strings.Count(got, `"`); n != 2 {
		t.Errorf("expected 2 quotes, got %d -- the note escaped its option:\n%s", n, got)
	}
	if n := strings.Count(got, ";"); n != 3 {
		t.Errorf("expected 3 semicolons (msg, sid, rev), got %d:\n%s", n, got)
	}
}
