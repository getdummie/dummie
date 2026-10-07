package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func self(t *testing.T) []byte {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tarball(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestCheckBinaryRejectsNonELF(t *testing.T) {
	if err := checkBinary([]byte("#!/bin/sh\necho hi\n")); err == nil {
		t.Fatal("a shell script passed as a binary")
	}
}

func TestFromTarball(t *testing.T) {
	got, err := fromTarball(tarball(t, "dinit", []byte("payload")), "dinit")
	if err != nil || string(got) != "payload" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := fromTarball(tarball(t, "other", []byte("x")), "dinit"); err == nil {
		t.Fatal("a tarball without dinit was accepted")
	}
}

func TestRunChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dinit")
	if err := os.WriteFile(src, []byte("not it"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "installed")
	err := run(context.Background(), "dev", src, "", hex.EncodeToString(make([]byte, 32)), dest)
	if err == nil {
		t.Fatal("a checksum mismatch was installed")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("dest was written despite the mismatch")
	}
}

// The test binary is built like dinit only when cgo is off, so this checks the
// install path rather than the static-link rule.
func TestInstallFromLocalTarball(t *testing.T) {
	bin := self(t)
	if checkBinary(bin) != nil {
		t.Skip("the test binary is dynamically linked")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "dinit.tar.gz")
	tb := tarball(t, "dinit", bin)
	if err := os.WriteFile(src, tb, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(tb)
	dest := filepath.Join(dir, "sbin-dinit")
	if err := run(context.Background(), "dev", src, "", hex.EncodeToString(sum[:]), dest); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dest)
	if err != nil || fi.Mode().Perm() != 0o755 || fi.Size() != int64(len(bin)) {
		t.Fatalf("installed %v, %v", fi, err)
	}
}
