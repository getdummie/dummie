package main

import (
  "bytes"
  "crypto/ed25519"
  "os"
  "path/filepath"
  "strings"
  "testing"

  "golang.org/x/crypto/ssh"
)

// pubOf reads a .pub file and returns the wire bytes of the key in it, so two
// files can be compared without caring about the comment.
func pubOf(t *testing.T, path string) []byte {
  t.Helper()
  b, err := os.ReadFile(path)
  if err != nil {
    t.Fatalf("could not read %s: %v", path, err)
  }
  key, _, _, _, err := ssh.ParseAuthorizedKey(b)
  if err != nil {
    t.Fatalf("%s is not a valid authorized_keys line (%v): %q", path, err, b)
  }
  return key.Marshal()
}

func TestEnsureEd25519KeypairGenerates(t *testing.T) {
  path := filepath.Join(t.TempDir(), "dpipe_host_ed25519")

  if err := ensureEd25519Keypair(path); err != nil {
    t.Fatalf("ensureEd25519Keypair: %v", err)
  }

  // The private key is only as good as its mode: sshd and dpipe both refuse a
  // key anyone else on the box can read.
  fi, err := os.Stat(path)
  if err != nil {
    t.Fatalf("stat: %v", err)
  }
  if fi.Mode().Perm() != 0o600 {
    t.Errorf("%s is %v, want 0600", path, fi.Mode().Perm())
  }

  priv, err := readEd25519PrivateKey(path)
  if err != nil {
    t.Fatalf("the key it just wrote does not parse: %v", err)
  }
  if priv == nil {
    t.Fatal("no private key was written")
  }

  // The .pub has to be the public half of that exact private key, or dpipe
  // presents an identity guests were not built to accept.
  want, err := ssh.NewPublicKey(priv.Public().(ed25519.PublicKey))
  if err != nil {
    t.Fatalf("could not derive the public key: %v", err)
  }
  if !bytes.Equal(pubOf(t, path+".pub"), want.Marshal()) {
    t.Error("the .pub does not match the private key beside it")
  }
}

// Rotation is an operator's decision: the host key is pinned by clients and the
// client key is already baked into built images.
func TestEnsureEd25519KeypairLeavesAnExistingKeyAlone(t *testing.T) {
  path := filepath.Join(t.TempDir(), "dpipe_client_ed25519")

  if err := ensureEd25519Keypair(path); err != nil {
    t.Fatalf("first call: %v", err)
  }
  before, err := os.ReadFile(path)
  if err != nil {
    t.Fatal(err)
  }
  beforePub := pubOf(t, path+".pub")

  if err := ensureEd25519Keypair(path); err != nil {
    t.Fatalf("second call: %v", err)
  }
  after, err := os.ReadFile(path)
  if err != nil {
    t.Fatal(err)
  }
  if !bytes.Equal(before, after) {
    t.Error("the private key was replaced on a second run")
  }
  if !bytes.Equal(beforePub, pubOf(t, path+".pub")) {
    t.Error("the public key changed on a second run")
  }
}

// Without the .pub, no guest image gets dpipe's key installed -- and it is
// derivable, so refusing to start over it would be a self-inflicted outage.
func TestEnsureEd25519KeypairRederivesAMissingPub(t *testing.T) {
  path := filepath.Join(t.TempDir(), "dpipe_client_ed25519")

  if err := ensureEd25519Keypair(path); err != nil {
    t.Fatalf("first call: %v", err)
  }
  privBefore, err := os.ReadFile(path)
  if err != nil {
    t.Fatal(err)
  }
  wantPub := pubOf(t, path+".pub")
  if err := os.Remove(path + ".pub"); err != nil {
    t.Fatal(err)
  }

  if err := ensureEd25519Keypair(path); err != nil {
    t.Fatalf("second call: %v", err)
  }
  if !bytes.Equal(wantPub, pubOf(t, path+".pub")) {
    t.Error("the re-derived .pub is not the one that belongs to this private key")
  }
  after, err := os.ReadFile(path)
  if err != nil {
    t.Fatal(err)
  }
  if !bytes.Equal(privBefore, after) {
    t.Error("re-deriving the .pub rewrote the private key")
  }
}

// Overwriting a key an operator placed by hand is how a fleet loses access to
// its own guests, so an unreadable one is a hard stop instead.
func TestEnsureEd25519KeypairRefusesToClobberAnUnusableKey(t *testing.T) {
  path := filepath.Join(t.TempDir(), "dpipe_host_ed25519")
  const junk = "this is not a private key\n"
  if err := os.WriteFile(path, []byte(junk), 0o600); err != nil {
    t.Fatal(err)
  }

  err := ensureEd25519Keypair(path)
  if err == nil {
    t.Fatal("an unparseable key was accepted")
  }
  if !strings.Contains(err.Error(), path) {
    t.Errorf("the error does not name the file: %v", err)
  }
  b, readErr := os.ReadFile(path)
  if readErr != nil {
    t.Fatal(readErr)
  }
  if string(b) != junk {
    t.Error("the existing file was overwritten")
  }
}
