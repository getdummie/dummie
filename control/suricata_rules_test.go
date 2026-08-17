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

// noTargets is what the LEFT JOIN returns for a VM that has no allowances: the
// VM's own columns and nothing else.
func noTargets(ip, name string) db.ListVMNetworkTargetsByClientRow {
	return db.ListVMNetworkTargetsByClientRow{VMIP: ip, VMName: name, HostVMID: "vm-" + name}
}

// The floor has to be there whatever else is, including when nothing is: a host
// with no allowances at all must still get a file that denies, not an empty one
// that permits by having no opinion.
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
				"drop http $HOME_NET any -> any any",
				// The positive protocol allowlist. Without the two negations this rule
				// would only catch traffic detection gave up on, and anything suricata
				// could actually name -- ssh on 443 -- would pass.
				"drop tcp $HOME_NET any -> any [80,443]",
				"app-layer-protocol:!http; app-layer-protocol:!tls;",
				// It must not be able to fire on the handshake, or every legitimate
				// request dies at the syn.
				"dsize:>0;",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("generated ruleset is missing the deny %q:\n%s", want, out)
				}
			}
		})
	}
}

// The whole point of the source address in each header: one VM's allowance must
// not be usable by the VM next to it.
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

// Guest dns is answered by the resolver on the gateway and never reaches the
// forward chain. A pass dns rule here would be a way to talk dns to any server
// on the internet, with an allowed name as the password.
func TestGenerateSuricataRulesNeverPassesDNS(t *testing.T) {
	out := generateSuricataRules([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
	})
	if strings.Contains(out, "pass dns") {
		t.Errorf("a pass dns rule was generated:\n%s", out)
	}
}

// The blanket deny is what closes the completed-handshake hole, and which VMs get
// it is the whole subtlety: one that has a rule needing to read a hostname cannot
// have it, and one that does not, must.
func TestGenerateSuricataRulesBlanketDeniesWithoutANameRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []db.ListVMNetworkTargetsByClientRow
		want bool
	}{
		{"no targets at all", []db.ListVMNetworkTargetsByClientRow{noTargets("10.0.0.2", "alpha")}, true},
		{"addresses only",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "ip", "1.2.3.4", "tcp", "22")}, true},
		// Lookup-only grants no web access, so nothing it compiles to needs to see a
		// handshake either.
		{"lookup-only domain",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "domain", "example.com", "", "none")}, true},
		{"a domain on both web ports",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "domain", "example.com", "", "")}, false},
		{"a domain on one web port",
			[]db.ListVMNetworkTargetsByClientRow{row("10.0.0.2", "alpha", "domain", "example.com", "", "443")}, false},
		// One name-based allowance is enough to need the syn, whatever else is there.
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

// A VM that is denied everything still has to reach the generator, and the one
// thing that could silently stop it is the join: an inner one drops it, and the
// VM keeps the syn on the web ports with nobody able to see why.
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

// A duplicated sid makes Suricata drop one of the two rules, silently revoking
// an allowance somebody was told they had.
func TestGenerateSuricataRulesSidsAreUnique(t *testing.T) {
	out := generateSuricataRules([]db.ListVMNetworkTargetsByClientRow{
		row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
		row("10.0.0.2", "alpha", "domain", "other.com", "", ""),
		row("10.0.0.3", "beta", "ip", "1.2.3.4", "any", "80,443"),
		row("10.0.0.3", "beta", "ip", "10.0.0.0/8", "any", ""),
		// Two VMs whose only rule is a blanket deny: those consume sids from the
		// same counter as the pass rules do.
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
		// "either transport" is `ip` in a rule header, but a header cannot carry a
		// port on `ip` -- so a ported allowance becomes one rule per transport.
		{"any transport, no port",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "any", ""),
			[]string{`pass ip 10.0.0.2 any -> 1.2.3.4 any`}},
		{"any transport with ports",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "any", "8080"),
			[]string{`pass tcp 10.0.0.2 any -> 1.2.3.4 8080`, `pass udp 10.0.0.2 any -> 1.2.3.4 8080`}},
		// A bare `80,443` is a parse error that would take the whole file down.
		{"port list is bracketed",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "tcp", "80,443"),
			[]string{`pass tcp 10.0.0.2 any -> 1.2.3.4 [80,443]`}},
		{"port range is not",
			row("10.0.0.2", "a", "ip", "1.2.3.4", "udp", "1000:2000"),
			[]string{`pass udp 10.0.0.2 any -> 1.2.3.4 1000:2000`}},
		// icmp takes the port slot as `any` and means it, and must not be widened to
		// `ip` -- which would open every tcp and udp port to the same address.
		{"icmp",
			row("10.0.0.2", "a", "ip", "8.8.8.8", "icmp", ""),
			[]string{`pass icmp 10.0.0.2 any -> 8.8.8.8 any`}},
		// dotprefix is what stops example.com from also allowing notexample.com.
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

// Which web ports a domain is allowed on. 'none' is the interesting one: it has
// to compile to no rule at all, because its whole purpose is to let the resolver
// answer a name without granting anything on the wire.
// An icmp allowance must not become an `ip` one. `pass ip` would let the guest
// reach every tcp and udp port on that address, which is not what a row saying
// "icmp" says, and the difference is invisible in a rule that loads either way.
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
			// A rule that consumed a sid it never emitted would eventually collide
			// with one that did.
			if want := suricataPassSidBase + len(tc.want); sid != want {
				t.Errorf("sid counter is at %d after %d rules, want %d", sid, len(got), want)
			}
		})
	}
}

// A note is the one field a user types freely, and it lands inside a rule
// option. An unescaped quote or semicolon there is a ruleset Suricata refuses to
// load -- taking every other VM on the host with it.
func TestRuleTextCannotBreakOutOfAnOption(t *testing.T) {
	r := row("10.0.0.2", "a", "ip", "1.2.3.4", "tcp", "443")
	r.Note = `bad"; drop ip any any -> any any (sid:1;)`

	sid := suricataPassSidBase
	got := strings.Join(targetRules(r, &sid), "\n")

	// Exactly the two that open and close msg: any more and the note closed it
	// early and started writing rule syntax of its own.
	if n := strings.Count(got, `"`); n != 2 {
		t.Errorf("expected 2 quotes, got %d -- the note escaped its option:\n%s", n, got)
	}
	// The options after msg are fixed, so the only semicolons left are theirs.
	if n := strings.Count(got, ";"); n != 3 {
		t.Errorf("expected 3 semicolons (msg, sid, rev), got %d:\n%s", n, got)
	}
}
