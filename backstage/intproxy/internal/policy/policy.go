// Package policy decides what a client may reach when this proxy holds the
// credentials itself. Under broker mode there is no policy here at all: the
// control server decides on every request, so a stale local copy cannot exist.
package policy

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"path"
	"strings"

	"intproxy/internal/credential"
)

type Policy struct {
	// Default exists to reject a typo loudly. Only "deny" is meaningful: an
	// allow-all has no credential to pick, so there is nothing to grant.
	Default string   `yaml:"default"`
	Clients []Client `yaml:"clients"`
}

type Client struct {
	Name string `yaml:"name"`
	// Source is an address or CIDR. Identity is the source address of the
	// connection and nothing else, so two people sharing a host share a client.
	Source       string              `yaml:"source"`
	Integrations []ClientIntegration `yaml:"integrations"`

	prefix netip.Prefix
}

type ClientIntegration struct {
	Name string `yaml:"name"`
	// Credential names which configured credential this client's requests use.
	Credential string `yaml:"credential"`
	// Read and Write are glob patterns over the integration's resource, a
	// repository slug for github. Both empty forwards everything and lets the
	// upstream decide, which is the right shape for a fine-grained token whose
	// scope is already the boundary.
	Read  []string `yaml:"read"`
	Write []string `yaml:"write"`
	// Unscoped allows requests that name no resource. They cannot be authorized
	// per resource, so they are off unless asked for.
	Unscoped *bool `yaml:"unscoped"`
}

type Decision struct {
	Client     string
	Credential string
}

func (p *Policy) Compile() error {
	switch p.Default {
	case "", "deny":
	case "allow":
		return errors.New(`policy: default "allow" is not supported; list the clients that may connect`)
	default:
		return fmt.Errorf("policy: default %q must be deny", p.Default)
	}
	if len(p.Clients) == 0 {
		return errors.New("policy: at least one client is required, or nothing can connect")
	}

	seen := map[string]bool{}
	for i := range p.Clients {
		c := &p.Clients[i]
		if c.Source == "" {
			return fmt.Errorf("policy: client %q has no source", c.Name)
		}
		prefix, err := parseSource(c.Source)
		if err != nil {
			return fmt.Errorf("policy: client %q: %w", c.Name, err)
		}
		c.prefix = prefix

		if c.Name == "" {
			c.Name = c.Source
		}
		if seen[c.Name] {
			return fmt.Errorf("policy: client %q is listed twice", c.Name)
		}
		seen[c.Name] = true

		if len(c.Integrations) == 0 {
			return fmt.Errorf("policy: client %q names no integrations", c.Name)
		}
		for _, ci := range c.Integrations {
			if ci.Name == "" {
				return fmt.Errorf("policy: client %q has an integration with no name", c.Name)
			}
		}
	}
	return nil
}

// parseSource takes a CIDR or a bare address, which becomes a host route.
func parseSource(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is neither an address nor a CIDR", s)
	}
	return netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()), nil
}

// Allow reports whether this client may make this request, and with which
// credential. The first client whose source contains the address wins, so
// order the list from most specific to least.
func (p *Policy) Allow(ip netip.Addr, integrationName, resource string, write bool) (Decision, error) {
	for i := range p.Clients {
		c := &p.Clients[i]
		if !c.prefix.Contains(ip) {
			continue
		}

		ci := c.integration(integrationName)
		if ci == nil {
			return Decision{}, credential.Denyf(http.StatusForbidden,
				"intproxy: %s is not allowed to use the %s integration", c.Name, integrationName)
		}
		d := Decision{Client: c.Name, Credential: ci.Credential}

		if resource == "" {
			if ci.Unscoped == nil || !*ci.Unscoped {
				return Decision{}, credential.Denyf(http.StatusForbidden,
					"intproxy: requests that name no repository are off for %s; set unscoped: true to allow them", c.Name)
			}
			return d, nil
		}

		// No lists at all means the token's own scope is the boundary.
		if len(ci.Read) == 0 && len(ci.Write) == 0 {
			return d, nil
		}
		if write {
			if !match(ci.Write, resource) {
				return Decision{}, credential.Denyf(http.StatusForbidden,
					"intproxy: %s may not write to %s", c.Name, resource)
			}
			return d, nil
		}
		// Write access implies read access, so a repo listed only under write
		// is still clonable.
		if !match(ci.Read, resource) && !match(ci.Write, resource) {
			return Decision{}, credential.Denyf(http.StatusForbidden,
				"intproxy: %s may not read %s", c.Name, resource)
		}
		return d, nil
	}

	return Decision{}, credential.Denyf(http.StatusForbidden,
		"intproxy: no policy client matches %s", ip)
}

func (c *Client) integration(name string) *ClientIntegration {
	for i := range c.Integrations {
		if c.Integrations[i].Name == name {
			return &c.Integrations[i]
		}
	}
	return nil
}

// match is case-insensitive because github treats owner and repository names
// that way, and a pattern that only worked in one casing would be a trap.
func match(patterns []string, resource string) bool {
	resource = strings.ToLower(resource)
	for _, p := range patterns {
		if ok, err := path.Match(strings.ToLower(p), resource); err == nil && ok {
			return true
		}
	}
	return false
}
