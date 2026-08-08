package main

import (
  "strings"
  "testing"

  "gopkg.in/yaml.v3"

  "control/internal/db"
)

// parsedProxyConfig is the part of the generated file these tests assert on.
// Parsed rather than string-matched, because what matters is what proxy reads,
// not how it was laid out.
type parsedProxyConfig struct {
  SSH struct {
    Listen string `yaml:"listen"`
    Users  []struct {
      Pubkey     string `yaml:"pubkey"`
      Target     string `yaml:"target"`
      RemoteUser string `yaml:"remote_user"`
    } `yaml:"users"`
  } `yaml:"ssh"`
}

func parseProxyConfig(t *testing.T, out string) parsedProxyConfig {
  t.Helper()
  var got parsedProxyConfig
  if err := yaml.Unmarshal([]byte(out), &got); err != nil {
    t.Fatalf("the generated config is not valid yaml (%v):\n%s", err, out)
  }
  return got
}

func TestGenerateProxyConfigOneVMPerUser(t *testing.T) {
  rows := []db.ListProxySSHUsersByAgentRow{
    {VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one"},
    {VMIP: "10.64.0.3", HostVMID: "def456", VMName: "test", PublicKey: "ssh-ed25519 AAAAC3Ny two"},
  }

  got := parseProxyConfig(t, generateProxyConfig(rows))
  if got.SSH.Listen != proxySSHListen {
    t.Errorf("listen is %q, want %q", got.SSH.Listen, proxySSHListen)
  }
  if len(got.SSH.Users) != 2 {
    t.Fatalf("got %d users, want 2", len(got.SSH.Users))
  }
  for i, want := range []struct{ pubkey, target string }{
    {"ssh-ed25519 AAAAC3Nz one", "10.64.0.2:22"},
    {"ssh-ed25519 AAAAC3Ny two", "10.64.0.3:22"},
  } {
    u := got.SSH.Users[i]
    if u.Pubkey != want.pubkey {
      t.Errorf("user %d pubkey is %q, want %q", i, u.Pubkey, want.pubkey)
    }
    if u.Target != want.target {
      t.Errorf("user %d target is %q, want %q", i, u.Target, want.target)
    }
    if u.RemoteUser != proxyRemoteUser {
      t.Errorf("user %d remote_user is %q, want %q", i, u.RemoteUser, proxyRemoteUser)
    }
  }
}

// A host whose VMs were all destroyed must produce an empty list, not a missing
// key: "nobody may connect" and "not configured" should not look alike.
func TestGenerateProxyConfigEmptyHost(t *testing.T) {
  out := generateProxyConfig(nil)
  if !strings.Contains(out, "users: []") {
    t.Errorf("an empty host did not emit an empty user list:\n%s", out)
  }
  got := parseProxyConfig(t, out)
  if len(got.SSH.Users) != 0 {
    t.Errorf("got %d users, want 0", len(got.SSH.Users))
  }
}

// The comment on a public key is free text the user typed. Unquoted, a " #" in
// it would truncate the scalar and leave a valid-looking key that is not the one
// on file -- so the value has to survive the round trip intact.
func TestGenerateProxyConfigQuotesAwkwardComments(t *testing.T) {
  const awkward = `ssh-ed25519 AAAAC3Nz me@host # not a comment: "quoted" \ and: more`
  rows := []db.ListProxySSHUsersByAgentRow{
    {VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: awkward},
  }

  got := parseProxyConfig(t, generateProxyConfig(rows))
  if len(got.SSH.Users) != 1 {
    t.Fatalf("got %d users, want 1", len(got.SSH.Users))
  }
  if got.SSH.Users[0].Pubkey != awkward {
    t.Errorf("the key did not survive:\n got %q\nwant %q", got.SSH.Users[0].Pubkey, awkward)
  }
}

// A VM name ends up in a '#' comment line. A newline in one would end the
// comment and put whatever followed into the document as configuration.
func TestGenerateProxyConfigNeutralisesNamesInComments(t *testing.T) {
  rows := []db.ListProxySSHUsersByAgentRow{
    {
      VMIP:      "10.64.0.2",
      HostVMID:  "abc123",
      VMName:    "evil\nssh:\n  users: []\n",
      PublicKey: "ssh-ed25519 AAAAC3Nz one",
    },
  }

  got := parseProxyConfig(t, generateProxyConfig(rows))
  if len(got.SSH.Users) != 1 {
    t.Fatalf("a name broke out of its comment and changed the document: got %d users, want 1", len(got.SSH.Users))
  }
}

// The agent skips the restart when the file is unchanged, which only works if
// the same fleet compiles to the same bytes every time.
func TestGenerateProxyConfigIsDeterministic(t *testing.T) {
  rows := []db.ListProxySSHUsersByAgentRow{
    {VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one"},
    {VMIP: "10.64.0.3", HostVMID: "def456", VMName: "test", PublicKey: "ssh-ed25519 AAAAC3Ny two"},
  }
  if generateProxyConfig(rows) != generateProxyConfig(rows) {
    t.Error("two runs over the same rows produced different files")
  }
}
