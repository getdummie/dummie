package dinit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSectorCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "size")
	if err := os.WriteFile(path, []byte("12582912\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readSectorCount(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(6 << 30); got != want {
		t.Fatalf("readSectorCount = %d, want %d", got, want)
	}

	if _, err := readSectorCount(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected an error for a missing size file")
	}

	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, []byte("not a number"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSectorCount(bad); err == nil {
		t.Fatal("expected an error for a non-numeric size file")
	}
}
