package main

import (
  "strings"
  "testing"

  "control/internal/db"
)

func row(ip, name, kind, dest, transport, ports string) db.ListVMNetworkTargetsByAgentRow {
  return db.ListVMNetworkTargetsByAgentRow{
    VMIP: ip, VMName: name, HostVMID: "vm-" + name,
    Kind: kind, Destination: dest, Transport: transport, Ports: ports,
  }
}

// The floor has to be there whatever else is, including when nothing is: a host
// with no allowances at all must still get a file that denies, not an empty one
// that permits by having no opinion.
func TestGenerateSuricataRulesAlwaysDenies(t *testing.T) {
  for _, tc := range []struct {
    name string
    rows []db.ListVMNetworkTargetsByAgentRow
  }{
    {"no targets", nil},
    {"some targets", []db.ListVMNetworkTargetsByAgentRow{row("10.0.0.2", "a", "domain", "example.com", "", "")}},
  } {
    t.Run(tc.name, func(t *testing.T) {
      out := generateSuricataRules(tc.rows)
      for _, want := range []string{
        "drop ip $HOME_NET any -> any any",
        "drop tcp $HOME_NET any -> any ![80,443]",
        "drop tls $HOME_NET any -> any any",
        "drop http $HOME_NET any -> any any",
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
  out := generateSuricataRules([]db.ListVMNetworkTargetsByAgentRow{
    row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
    row("10.0.0.3", "beta", "ip", "1.2.3.4", "tcp", "443"),
  })

  for _, want := range []string{
    `pass dns 10.0.0.2 any -> any any`,
    `pass tls 10.0.0.2 any -> any any`,
    `pass http 10.0.0.2 any -> any any`,
    `pass tcp 10.0.0.3 any -> 1.2.3.4 443`,
  } {
    if !strings.Contains(out, want) {
      t.Errorf("missing %q:\n%s", want, out)
    }
  }
  if strings.Contains(out, "pass dns $HOME_NET") || strings.Contains(out, "pass tcp $HOME_NET") {
    t.Errorf("a pass rule was written against the whole pool:\n%s", out)
  }
}

// A duplicated sid makes Suricata drop one of the two rules, silently revoking
// an allowance somebody was told they had.
func TestGenerateSuricataRulesSidsAreUnique(t *testing.T) {
  out := generateSuricataRules([]db.ListVMNetworkTargetsByAgentRow{
    row("10.0.0.2", "alpha", "domain", "example.com", "", ""),
    row("10.0.0.2", "alpha", "domain", "other.com", "", ""),
    row("10.0.0.3", "beta", "ip", "1.2.3.4", "any", "80,443"),
    row("10.0.0.3", "beta", "ip", "10.0.0.0/8", "any", ""),
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
    row   db.ListVMNetworkTargetsByAgentRow
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
    // dotprefix is what stops example.com from also allowing notexample.com.
    {"domain matches subdomains only",
      row("10.0.0.2", "a", "domain", "example.com", "", ""),
      []string{`dns.query; dotprefix; content:".example.com"; nocase; endswith;`,
        `tls.sni; dotprefix; content:".example.com"; nocase; endswith;`,
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
