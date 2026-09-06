package main

import (
	"os"
	"path/filepath"
	"testing"

	"control/internal/proto"
)

func testKeys() *proto.DpipeSSHKeys {
	return &proto.DpipeSSHKeys{
		HostKey:   "-----BEGIN OPENSSH PRIVATE KEY-----\nhost\n-----END OPENSSH PRIVATE KEY-----\n",
		HostPub:   "ssh-ed25519 AAAAhost dpipe-host\n",
		ClientKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nclient\n-----END OPENSSH PRIVATE KEY-----\n",
		ClientPub: "ssh-ed25519 AAAAclient dpipe-client\n",
	}
}

func TestWriteDpipeKeysInstallsWhatControlSent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")

	changed, err := writeDpipeKeysTo(dir, testKeys())
	if err != nil {
		t.Fatalf("writeDpipeKeysTo: %v", err)
	}
	if !changed {
		t.Error("writing the keys for the first time did not report a change")
	}

	for path, want := range map[string]string{
		filepath.Join(dir, dpipeHostKeyName):          testKeys().HostKey,
		filepath.Join(dir, dpipeHostKeyName+".pub"):   testKeys().HostPub,
		filepath.Join(dir, dpipeClientKeyName):        testKeys().ClientKey,
		filepath.Join(dir, dpipeClientKeyName+".pub"): testKeys().ClientPub,
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("could not read %s: %v", path, err)
		}
		if string(b) != want {
			t.Errorf("%s = %q, want %q", path, b, want)
		}
	}

	for _, name := range []string{dpipeHostKeyName, dpipeClientKeyName} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v, want 0600", name, fi.Mode().Perm())
		}
	}
}

func TestWriteDpipeKeysIsQuietWhenNothingChanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")

	if _, err := writeDpipeKeysTo(dir, testKeys()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	changed, err := writeDpipeKeysTo(dir, testKeys())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if changed {
		t.Error("rewriting the same keys reported a change, which would restart dpipe for nothing")
	}
}

func TestWriteDpipeKeysReplacesAnOlderKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")

	stale := testKeys()
	stale.HostKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nstale\n-----END OPENSSH PRIVATE KEY-----\n"
	if _, err := writeDpipeKeysTo(dir, stale); err != nil {
		t.Fatalf("first call: %v", err)
	}

	changed, err := writeDpipeKeysTo(dir, testKeys())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !changed {
		t.Error("a new host key was not reported as a change")
	}
	b, err := os.ReadFile(filepath.Join(dir, dpipeHostKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != testKeys().HostKey {
		t.Error("the stale host key was left in place")
	}
}

func TestWriteDpipeKeysRefusesAnIncompleteSet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")

	keys := testKeys()
	keys.ClientKey = ""
	if _, err := writeDpipeKeysTo(dir, keys); err == nil {
		t.Fatal("a set with no client key was accepted")
	}
}

func TestWriteDpipeKeysWithoutKeysDoesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")

	changed, err := writeDpipeKeysTo(dir, nil)
	if err != nil {
		t.Fatalf("writeDpipeKeysTo(nil): %v", err)
	}
	if changed {
		t.Error("a job carrying no keys reported a change")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a job carrying no keys created the key directory")
	}
}
