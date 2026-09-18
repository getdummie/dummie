package policy

import (
	"net/netip"
	"strings"
	"testing"
)

func compiled(t *testing.T, p *Policy) *Policy {
	t.Helper()
	if err := p.Compile(); err != nil {
		t.Fatal(err)
	}
	return p
}

func addr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func basic() *Policy {
	return &Policy{Clients: []Client{{
		Name:   "build",
		Source: "10.64.0.0/24",
		Integrations: []ClientIntegration{{
			Name:       "github",
			Credential: "ci",
			Read:       []string{"acme/*"},
			Write:      []string{"acme/releases"},
		}},
	}}}
}

func TestAllowsReadWithinTheGlob(t *testing.T) {
	p := compiled(t, basic())
	d, err := p.Allow(addr(t, "10.64.0.9"), "github", "acme/thing", false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Credential != "ci" || d.Client != "build" {
		t.Fatalf("decision = %+v", d)
	}
}

// A repository listed only under write is still clonable; the alternative is
// having to name it twice.
func TestWriteImpliesRead(t *testing.T) {
	p := compiled(t, basic())
	if _, err := p.Allow(addr(t, "10.64.0.9"), "github", "acme/releases", false); err != nil {
		t.Fatalf("a write-listed repo was not readable: %v", err)
	}
}

func TestDeniesWriteOutsideTheWriteList(t *testing.T) {
	p := compiled(t, basic())
	_, err := p.Allow(addr(t, "10.64.0.9"), "github", "acme/thing", true)
	if err == nil || !strings.Contains(err.Error(), "may not write") {
		t.Fatalf("a write to a read-only repo was allowed: %v", err)
	}
}

func TestDeniesUnmatchedSource(t *testing.T) {
	p := compiled(t, basic())
	_, err := p.Allow(addr(t, "192.0.2.1"), "github", "acme/thing", false)
	if err == nil || !strings.Contains(err.Error(), "no policy client matches") {
		t.Fatalf("an unlisted source was allowed: %v", err)
	}
}

func TestDeniesUnlistedIntegration(t *testing.T) {
	p := compiled(t, basic())
	_, err := p.Allow(addr(t, "10.64.0.9"), "llm", "", false)
	if err == nil || !strings.Contains(err.Error(), "not allowed to use") {
		t.Fatalf("an unlisted integration was allowed: %v", err)
	}
}

// A request that names no repository cannot be scoped to one, so it is off
// unless the operator asks for it.
func TestUnscopedIsOffByDefault(t *testing.T) {
	p := compiled(t, basic())
	if _, err := p.Allow(addr(t, "10.64.0.9"), "github", "", false); err == nil {
		t.Fatal("an unscoped request was allowed without unscoped: true")
	}

	yes := true
	p = basic()
	p.Clients[0].Integrations[0].Unscoped = &yes
	p = compiled(t, p)
	if _, err := p.Allow(addr(t, "10.64.0.9"), "github", "", false); err != nil {
		t.Fatalf("unscoped: true still refused: %v", err)
	}
}

// With no lists at all the token's own scope is the boundary, which is the
// right shape for a fine-grained personal access token.
func TestNoListsForwardsEverything(t *testing.T) {
	p := compiled(t, &Policy{Clients: []Client{{
		Name:         "alice",
		Source:       "10.64.0.5",
		Integrations: []ClientIntegration{{Name: "github", Credential: "alice"}},
	}}})

	for _, write := range []bool{false, true} {
		if _, err := p.Allow(addr(t, "10.64.0.5"), "github", "anyone/anything", write); err != nil {
			t.Fatalf("write=%v was refused: %v", write, err)
		}
	}
}

// github treats owner and repository names case-insensitively, so a pattern
// that only worked in one casing would be a trap.
func TestMatchingIsCaseInsensitive(t *testing.T) {
	p := compiled(t, basic())
	if _, err := p.Allow(addr(t, "10.64.0.9"), "github", "ACME/Thing", false); err != nil {
		t.Fatalf("a differently cased name was refused: %v", err)
	}
}

func TestCompileRejects(t *testing.T) {
	cases := []struct {
		name string
		p    *Policy
		want string
	}{
		{"no clients", &Policy{}, "at least one client"},
		{"default allow", &Policy{Default: "allow", Clients: basic().Clients}, `default "allow" is not supported`},
		{"no source", &Policy{Clients: []Client{{Name: "a"}}}, "has no source"},
		{"bad source", &Policy{Clients: []Client{{Name: "a", Source: "not-an-ip"}}}, "neither an address nor a CIDR"},
		{"no integrations", &Policy{Clients: []Client{{Name: "a", Source: "10.0.0.1"}}}, "names no integrations"},
		{"duplicate", &Policy{Clients: []Client{
			{Name: "a", Source: "10.0.0.1", Integrations: []ClientIntegration{{Name: "github"}}},
			{Name: "a", Source: "10.0.0.2", Integrations: []ClientIntegration{{Name: "github"}}},
		}}, "listed twice"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Compile()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
