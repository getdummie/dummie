package dinit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFindInitScript(t *testing.T) {
	sys, dev := t.TempDir(), t.TempDir()
	script := []byte("#!/bin/sh\necho hi\n")
	disks := map[string][]byte{
		"vda":   bytes.Repeat([]byte{0}, 4096),
		"vdb":   append([]byte(fmt.Sprintf("%s%d\n", initScriptMagic, len(script))), append(script, make([]byte, 100)...)...),
		"loop0": []byte(initScriptMagic + "1\nx"),
	}
	for name, body := range disks {
		if err := os.Mkdir(filepath.Join(sys, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dev, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := findInitScript(sys, dev)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, script) {
		t.Errorf("got %q, want %q", got, script)
	}
}

func TestReadInitScriptRejectsBadLength(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vdb")
	if err := os.WriteFile(p, []byte(fmt.Sprintf("%s%d\nabc", initScriptMagic, initScriptMax+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readInitScript(p); err == nil {
		t.Error("an oversized length was accepted")
	}
}
