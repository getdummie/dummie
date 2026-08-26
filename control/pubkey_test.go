package main

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testKey builds a real ed25519 authorized_keys line from a fixed seed, so the
// tests exercise the actual parser rather than a hand-copied blob that could be
// subtly wrong for reasons unrelated to what is being tested.
func testKey(t *testing.T, seedByte byte) string {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = seedByte
	}
	pub, err := ssh.NewPublicKey(ed25519.NewKeyFromSeed(seed).Public())
	if err != nil {
		t.Fatalf("could not build a test key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
}

// The comment is dropped, not kept: users.public_key is unique, and the same key
// under two comments has to be one value.
func TestNormalizePublicKeyDropsTheComment(t *testing.T) {
	key := testKey(t, 1)

	got, err := normalizePublicKey("  " + key + " me@laptop\n")
	if err != nil {
		t.Fatalf("normalizePublicKey: %v", err)
	}
	if got != key {
		t.Errorf("got %q, want %q", got, key)
	}

	same, err := normalizePublicKey(key + " someone-else@host")
	if err != nil {
		t.Fatalf("normalizePublicKey: %v", err)
	}
	if same != got {
		t.Errorf("the same key under two comments produced %q and %q", got, same)
	}
}

func TestNormalizePublicKeyEmptyIsNotAnError(t *testing.T) {
	got, err := normalizePublicKey("   \n ")
	if err != nil {
		t.Fatalf("an empty key is 'no key', not a bad request: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// Options are instructions to sshd. Dropping them is the point: a stored
// command="..." would change what happens when the key is used.
func TestNormalizePublicKeyDropsOptions(t *testing.T) {
	key := testKey(t, 2)

	got, err := normalizePublicKey(`command="/bin/sh",no-pty ` + key + " me@laptop")
	if err != nil {
		t.Fatalf("normalizePublicKey: %v", err)
	}
	if got != key {
		t.Errorf("got %q, want %q", got, key)
	}
}

// A pasted authorized_keys file must not be accepted as its first line, which is
// what the parser alone would do.
func TestNormalizePublicKeyRejectsASecondKey(t *testing.T) {
	two := testKey(t, 3) + " one\n" + testKey(t, 4) + " two\n"

	if _, err := normalizePublicKey(two); err == nil {
		t.Error("two keys were accepted; only one belongs to a user")
	}
}

func TestNormalizePublicKeyRejectsNonKeys(t *testing.T) {
	cases := map[string]string{
		"prose":                  "please let me in",
		"a private key":          "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaA==\n-----END OPENSSH PRIVATE KEY-----",
		"the type but no blob":   "ssh-ed25519",
		"a blob that is not one": "ssh-ed25519 not-base64!!",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizePublicKey(in); err == nil {
				t.Errorf("%q was accepted as a public key", in)
			}
		})
	}
}

func TestNormalizePublicKeyRejectsOversizedInput(t *testing.T) {
	if _, err := normalizePublicKey(strings.Repeat("a", maxPublicKeyLen+1)); err == nil {
		t.Error("an oversized input was accepted")
	}
}
