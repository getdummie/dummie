package proxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"testing"

	"golang.org/x/crypto/ssh"

	"proxy/internal/control"
)

func newTestKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}
	return sshPub
}

func authorizedLine(key ssh.PublicKey, comment string) string {
	return string(ssh.MarshalAuthorizedKey(key)) + comment
}

func newTestResolver(t *testing.T, cfg *Config) *Resolver {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r, err := NewResolver(log, NewRouter(cfg), cfg)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func TestResolveSSHKnownKey(t *testing.T) {
	alice := newTestKey(t)
	cfg := &Config{
		ControlSocket: "/tmp/ignored.sock",
		SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{{
			PubKey:     authorizedLine(alice, " alice@laptop"),
			Target:     "127.0.0.1:22",
			RemoteUser: "dev",
		}}},
	}
	r := newTestResolver(t, cfg)

	// The offered key carries a different comment: normalization must ignore it.
	rep := r.Handle(control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: "r1", Kind: control.KindSSH,
		SSHUser: "whoever", SSHPubKey: authorizedLine(alice, " someone-else@host"),
		SSHFingerprint: ssh.FingerprintSHA256(alice), ClientIP: "10.0.0.2",
	})
	if rep.Type != control.TypeResolved || !rep.Authorized {
		t.Fatalf("reply = %+v", rep)
	}
	if rep.Target != "127.0.0.1:22" || rep.RemoteUser != "dev" {
		t.Fatalf("target/remote_user = %q/%q", rep.Target, rep.RemoteUser)
	}
}

func TestResolveSSHUnknownKey(t *testing.T) {
	alice, bob := newTestKey(t), newTestKey(t)
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{{
		PubKey: authorizedLine(alice, ""), Target: "127.0.0.1:22", RemoteUser: "dev",
	}}}}
	r := newTestResolver(t, cfg)

	rep := r.Handle(control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: "r2", Kind: control.KindSSH,
		SSHPubKey: authorizedLine(bob, ""),
	})
	if rep.Type != control.TypeResolved || rep.Authorized {
		t.Fatalf("unknown key must not be authorized: %+v", rep)
	}
}

func TestResolveSSHUnparsableKeyFailsClosed(t *testing.T) {
	r := newTestResolver(t, &Config{})
	rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "r3",
		Kind: control.KindSSH, SSHPubKey: "not-a-key"})
	if rep.Authorized {
		t.Fatalf("expected unauthorized, got %+v", rep)
	}
}

func TestResolveHTTP(t *testing.T) {
	cfg := &Config{HTTP: &HTTPConfig{Hosts: map[string]string{"vm1.local": "127.0.0.1:8001"}}}
	r := newTestResolver(t, cfg)

	hit := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "h1",
		Kind: control.KindHTTP, Host: "vm1.local", SNI: "vm1.local"})
	if !hit.Authorized || hit.Target != "127.0.0.1:8001" {
		t.Fatalf("hit = %+v", hit)
	}

	miss := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "h2",
		Kind: control.KindHTTP, Host: "nope.local"})
	if miss.Authorized {
		t.Fatalf("miss must not be authorized: %+v", miss)
	}
}

func TestResolveUnknownKind(t *testing.T) {
	r := newTestResolver(t, &Config{})
	rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "k1", Kind: "smtp"})
	if rep.Type != control.TypeError {
		t.Fatalf("reply = %+v, want error", rep)
	}
}

func TestDuplicateKeyRejected(t *testing.T) {
	alice := newTestKey(t)
	line := authorizedLine(alice, "")
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{
		{PubKey: line, Target: "127.0.0.1:22", RemoteUser: "dev"},
		{PubKey: line, Target: "127.0.0.1:2200", RemoteUser: "root"},
	}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewResolver(log, NewRouter(cfg), cfg); err == nil {
		t.Fatal("expected duplicate public keys to be rejected")
	}
}
