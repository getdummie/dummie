package dinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadParams(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cmdline")
	line := "console=ttyS0 root=/dev/vda rw init=/sbin/dinit systemd.hostname=build " +
		"dclient.ip=10.64.0.7 dclient.gw=10.64.0.1 dclient.dns=10.64.0.2 quiet\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readParams(p)
	want := params{ip: "10.64.0.7", gateway: "10.64.0.1", dns: "10.64.0.2", hostname: "build"}
	if got != want {
		t.Errorf("readParams = %+v, want %+v", got, want)
	}
}

func TestReadParamsWithoutAnAddress(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cmdline")
	if err := os.WriteFile(p, []byte("console=ttyS0 root=/dev/vda rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readParams(p); got != (params{}) {
		t.Errorf("a cmdline with no dclient params yielded %+v", got)
	}
}

func TestEnsureHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1\tlocalhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ensureHosts(path, "build", "10.64.0.7")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "10.64.0.7\tbuild") || !strings.Contains(string(b), "localhost") {
		t.Errorf("hosts = %q", b)
	}

	ensureHosts(path, "build", "10.64.0.7")
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(b) {
		t.Errorf("a second boot appended the entry twice:\n%s", again)
	}
}

func TestEnsureHostsOnAnImageWithNoHostsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	ensureHosts(path, "build", "10.64.0.7")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "localhost") || !strings.Contains(string(b), "build") {
		t.Errorf("hosts = %q", b)
	}
}

func TestWriteFileLeavesSymlinkAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.Symlink("../run/systemd/resolve/stub-resolv.conf", path); err != nil {
		t.Fatal(err)
	}
	writeFile(path, "nameserver 10.0.0.1\n")
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced, taking resolv.conf away from whatever manages it")
	}
}

func TestShellArgv(t *testing.T) {
	if got := shellArgv("/bin/bash", nil); got[0] != "-bash" {
		t.Errorf("a shell session is not a login shell: %q", got)
	}
	if got := shellArgv("/bin/bash", []string{"-c", "uptime"}); got[0] != "bash" || len(got) != 3 {
		t.Errorf("exec argv = %q", got)
	}
	if got := shellArgv("/bin/busybox", nil); got[0] != "sh" {
		t.Errorf("busybox needs an applet name in argv[0], got %q", got)
	}
}

func TestLookupUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "passwd")
	body := "root:x:0:0:root:/root:/bin/bash\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	u, ok := lookupUserIn(path, "root")
	if !ok || u.uid != 0 || u.home != "/root" || u.shell != "/bin/bash" {
		t.Errorf("root = %+v (ok=%v)", u, ok)
	}
	if _, ok := lookupUserIn(path, "ubuntu"); ok {
		t.Error("an account the image does not have was found")
	}
}
