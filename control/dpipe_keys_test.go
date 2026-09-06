package main

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGeneratedSSHKeyParsesAndDerivesItsPub(t *testing.T) {
	pem, err := generateSSHPrivateKey("dpipe-host")
	if err != nil {
		t.Fatalf("generateSSHPrivateKey: %v", err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(pem))
	if err != nil {
		t.Fatalf("the generated key does not parse: %v", err)
	}

	line, err := sshPublicKeyLine(pem, "dpipe-host")
	if err != nil {
		t.Fatalf("sshPublicKeyLine: %v", err)
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatalf("%q is not an authorized_keys line: %v", line, err)
	}
	if comment != "dpipe-host" {
		t.Errorf("comment = %q, want dpipe-host", comment)
	}
	if string(pub.Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Error("the derived public key does not belong to the private key")
	}
	if !strings.HasSuffix(line, "\n") {
		t.Error("the authorized_keys line does not end in a newline")
	}
	if pub.Type() != ssh.KeyAlgoED25519 {
		t.Errorf("key type = %s, want %s", pub.Type(), ssh.KeyAlgoED25519)
	}
}

func TestGeneratedSSHKeysDiffer(t *testing.T) {
	a, err := generateSSHPrivateKey("dpipe-host")
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateSSHPrivateKey("dpipe-host")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two generated keys are identical")
	}
}
