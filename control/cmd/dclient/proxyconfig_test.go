package main

import (
	"os"
	"path/filepath"
	"testing"
)

const testCookieSecret = "0123456789abcdef0123456789abcdef"

func TestWriteProxyCookieSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "cookie_secret")

	changed, err := writeProxyCookieSecret(path, testCookieSecret)
	if err != nil {
		t.Fatalf("writeProxyCookieSecret: %v", err)
	}
	if !changed {
		t.Error("a first write reported no change, so proxy would not be restarted")
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read it back: %v", err)
	}
	if string(b) != testCookieSecret {
		t.Errorf("the file holds %d bytes, want the %d that were sent", len(b), len(testCookieSecret))
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode is %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("directory mode is %o, want 700", di.Mode().Perm())
	}
}

func TestWriteProxyCookieSecretUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie_secret")

	if _, err := writeProxyCookieSecret(path, testCookieSecret); err != nil {
		t.Fatalf("first write: %v", err)
	}
	changed, err := writeProxyCookieSecret(path, testCookieSecret)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if changed {
		t.Error("rewriting the same key reported a change, so proxy restarts on every push")
	}
}

func TestWriteProxyCookieSecretRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie_secret")

	if _, err := writeProxyCookieSecret(path, testCookieSecret); err != nil {
		t.Fatalf("first write: %v", err)
	}
	changed, err := writeProxyCookieSecret(path, "fedcba9876543210fedcba9876543210")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if !changed {
		t.Error("a rotated key reported no change, so proxy would keep verifying with the old one")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read it back: %v", err)
	}
	if string(b) != "fedcba9876543210fedcba9876543210" {
		t.Error("the rotated key was not the one written")
	}
}

func TestWriteProxyCookieSecretEmptyLeavesTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie_secret")
	if err := os.WriteFile(path, []byte(testCookieSecret), 0o600); err != nil {
		t.Fatalf("could not seed the file: %v", err)
	}

	changed, err := writeProxyCookieSecret(path, "")
	if err != nil {
		t.Fatalf("writeProxyCookieSecret: %v", err)
	}
	if changed {
		t.Error("an empty key reported a change")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read it back: %v", err)
	}
	if string(b) != testCookieSecret {
		t.Error("an empty key overwrote the one already on the host")
	}
}
