package credential

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

// TokenRef is where one token is read from. Never the config file itself: that
// file is world-readable in most deployments and, in a managed fleet, is
// written by something else entirely.
type TokenRef struct {
	File string `yaml:"token_file"`
	Env  string `yaml:"token_env"`
}

func (r TokenRef) empty() bool { return r == TokenRef{} }

// StaticConfig is an integration's `auth` block. Either one unnamed token, or
// a map of them so each client can bring its own.
type StaticConfig struct {
	Kind        string              `yaml:"kind"`
	Credentials map[string]TokenRef `yaml:"credentials"`
	Default     string              `yaml:"default"`

	TokenRef `yaml:",inline"`
}

func (c StaticConfig) Empty() bool {
	return len(c.Credentials) == 0 && c.TokenRef.empty()
}

const singleName = "default"

// Static serves tokens the operator configured. Unlike a minted installation
// token these carry whatever scope they were created with, so the proxy's own
// routing is the only thing narrowing them: prefer fine-grained tokens, whose
// scope GitHub enforces regardless of what this proxy forwards.
type Static struct {
	tokens map[string]string
	def    string
}

func NewStatic(cfg StaticConfig) (*Static, error) {
	switch cfg.Kind {
	case "", "token":
	default:
		return nil, fmt.Errorf("auth.kind %q is not supported yet; use \"token\"", cfg.Kind)
	}

	s := &Static{tokens: map[string]string{}, def: cfg.Default}

	for name, ref := range cfg.Credentials {
		v, err := ref.load(name)
		if err != nil {
			return nil, err
		}
		s.tokens[name] = v
	}

	if !cfg.TokenRef.empty() {
		if len(cfg.Credentials) > 0 {
			return nil, errors.New("auth: set either a single token_file/token_env or a credentials map, not both")
		}
		v, err := cfg.TokenRef.load(singleName)
		if err != nil {
			return nil, err
		}
		s.tokens[singleName] = v
		s.def = singleName
	}

	if len(s.tokens) == 0 {
		return nil, errors.New("auth: no credentials are configured")
	}
	if s.def == "" && len(s.tokens) == 1 {
		for name := range s.tokens {
			s.def = name
		}
	}
	if s.def != "" {
		if _, ok := s.tokens[s.def]; !ok {
			return nil, fmt.Errorf("auth: default credential %q is not in the credentials map", s.def)
		}
	}
	return s, nil
}

// Names is for logging what was loaded. It never returns a token.
func (s *Static) Names() []string {
	out := make([]string, 0, len(s.tokens))
	for name := range s.tokens {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *Static) Token(_ context.Context, sc Scope) (Token, error) {
	name := sc.Credential
	if name == "" {
		name = s.def
	}
	if name == "" {
		return Token{}, &Denial{
			Status:  http.StatusForbidden,
			Message: "intproxy: this client selects no credential and there is no default",
		}
	}
	v, ok := s.tokens[name]
	if !ok {
		return Token{}, Denyf(http.StatusForbidden, "intproxy: credential %q is not configured", name)
	}
	return Token{Value: v}, nil
}

func (r TokenRef) load(name string) (string, error) {
	switch {
	case r.File != "" && r.Env != "":
		return "", fmt.Errorf("credential %q: set token_file or token_env, not both", name)

	case r.Env != "":
		v := strings.TrimSpace(os.Getenv(r.Env))
		if v == "" {
			return "", fmt.Errorf("credential %q: %s is unset or empty", name, r.Env)
		}
		return v, nil

	case r.File != "":
		st, err := os.Stat(r.File)
		if err != nil {
			return "", fmt.Errorf("credential %q: %w", name, err)
		}
		if perm := st.Mode().Perm(); perm&0o077 != 0 {
			return "", fmt.Errorf("credential %q: %s is mode %04o, which group or other can read; chmod 600 it", name, r.File, perm)
		}
		b, err := os.ReadFile(r.File)
		if err != nil {
			return "", fmt.Errorf("credential %q: %w", name, err)
		}
		v := strings.TrimSpace(string(b))
		if v == "" {
			return "", fmt.Errorf("credential %q: %s is empty", name, r.File)
		}
		return v, nil
	}
	return "", fmt.Errorf("credential %q: neither token_file nor token_env is set", name)
}
