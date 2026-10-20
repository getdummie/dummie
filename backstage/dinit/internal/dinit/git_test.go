package dinit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadParamsGitIdentity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cmdline")
	line := "init=/sbin/dinit dclient.git.name=Jane%20%22J%22%20Doe dclient.git.email=jane@example.com\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readParams(p)
	if got.gitName != `Jane "J" Doe` || got.gitEmail != "jane@example.com" {
		t.Errorf("readParams = %+v", got)
	}
}

func TestWriteGitConfig(t *testing.T) {
	home := t.TempDir()
	u := user{name: "me", uid: os.Getuid(), gid: os.Getgid(), home: home}
	if err := writeGitConfig(params{gitName: `Jane "J" Doe`, gitEmail: "jane@example.com"}, u); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	want := "[user]\n\tname = \"Jane \\\"J\\\" Doe\"\n\temail = \"jane@example.com\"\n"
	if string(b) != want {
		t.Errorf(".gitconfig = %q, want %q", b, want)
	}
	if err := writeGitConfig(params{gitName: "x"}, u); err == nil {
		t.Error("an existing .gitconfig was overwritten")
	}
}
