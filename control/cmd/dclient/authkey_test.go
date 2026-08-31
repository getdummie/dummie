package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRoot(t *testing.T, passwd string, homes ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "passwd"), []byte(passwd), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, h := range homes {
		if err := os.MkdirAll(filepath.Join(root, h), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const ubuntuPasswd = "root:x:0:0:root:/root:/bin/bash\nubuntu:x:1000:1000:Ubuntu:/home/ubuntu:/bin/bash\n"

func TestAuthKeyTargets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		passwd  string
		homes   []string
		symlink string
		want    map[string]int
	}{
		{"both when the image has both", ubuntuPasswd,
			[]string{"root", "home/ubuntu"}, "", map[string]int{"ubuntu": 1000, "root": 0}},
		{"root alone when there is no ubuntu", "root:x:0:0:root:/root:/bin/bash\n",
			[]string{"root"}, "", map[string]int{"root": 0}},
		{"ubuntu is skipped when it has no home", ubuntuPasswd,
			[]string{"root"}, "", map[string]int{"root": 0}},
		{"root survives a missing home", ubuntuPasswd,
			[]string{"home/ubuntu"}, "", map[string]int{"ubuntu": 1000, "root": 0}},
		{"a symlinked home is not followed", ubuntuPasswd,
			[]string{"home/ubuntu"}, "root", map[string]int{"ubuntu": 1000}},
		{"uid is read from the image", "root:x:0:0:root:/root:/bin/sh\nubuntu:x:1500:1600:U:/home/ubuntu:/bin/sh\n",
			[]string{"root", "home/ubuntu"}, "", map[string]int{"ubuntu": 1500, "root": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fakeRoot(t, tc.passwd, tc.homes...)
			if tc.symlink != "" {
				if err := os.Symlink("/etc", filepath.Join(root, tc.symlink)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := authKeyTargets(root, authKeyAccounts)
			if err != nil {
				t.Fatalf("authKeyTargets: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d accounts, want %d: %+v", len(got), len(tc.want), got)
			}
			for _, u := range got {
				uid, ok := tc.want[u.name]
				if !ok {
					t.Errorf("unexpected account %s", u.name)
					continue
				}
				if u.uid != uid {
					t.Errorf("%s has uid %d, want %d", u.name, u.uid, uid)
				}
			}
		})
	}
}

func TestAuthKeyTargetsNeedAPasswd(t *testing.T) {
	if _, err := authKeyTargets(t.TempDir(), authKeyAccounts); err == nil {
		t.Error("expected an error for an image with no /etc/passwd")
	}
}

func TestImagePathCannotEscape(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"/../../etc", "../etc", "/home/../../.."} {
		if p, err := imagePath(root, bad); err == nil && !strings.HasPrefix(p, root) {
			t.Errorf("imagePath(%q) = %q, which is outside %q", bad, p, root)
		}
	}
	got, err := imagePath(root, "/home/ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "home", "ubuntu"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestKeyedDigest(t *testing.T) {
	a := keyedDigest("abc123", "ssh-ed25519 AAAA one")
	if a != keyedDigest("abc123", "ssh-ed25519 AAAA one") {
		t.Error("keyedDigest is not stable for the same inputs")
	}
	if a == keyedDigest("abc123", "ssh-ed25519 AAAA two") {
		t.Error("a different key produced the same cache entry")
	}
	if a == keyedDigest("def456", "ssh-ed25519 AAAA one") {
		t.Error("a different tar produced the same cache entry")
	}
	if a == "abc123" {
		t.Error("a keyed digest collided with the bare tar digest")
	}
}

func TestAuthKeyRecipeIsInTheCacheIdentity(t *testing.T) {
	const key = "ssh-ed25519 AAAA one"
	before := keyedDigest("abc123", imageRecipe(key, "", ""))

	restore := authKeyAccounts
	t.Cleanup(func() { authKeyAccounts = restore })
	authKeyAccounts = []string{"ubuntu"}

	if before == keyedDigest("abc123", imageRecipe(key, "", "")) {
		t.Error("changing the accounts left the cache entry the same")
	}
}

func TestInjectAuthorizedKeyAppends(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("injectAuthorizedKey chowns, which needs root")
	}
	root := fakeRoot(t, ubuntuPasswd, "root", "home/ubuntu")
	authKeys := filepath.Join(root, "home", "ubuntu", ".ssh", "authorized_keys")
	rootKeys := filepath.Join(root, "root", ".ssh", "authorized_keys")

	if err := os.MkdirAll(filepath.Dir(authKeys), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authKeys, []byte("ssh-rsa AAAA theirs\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const key = "ssh-ed25519 AAAA dpipe"
	if err := injectAuthorizedKey(root, key, ""); err != nil {
		t.Fatal(err)
	}
	if err := injectAuthorizedKey(root, key, ""); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(authKeys)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "ssh-rsa AAAA theirs") {
		t.Errorf("the image's own key was lost:\n%s", got)
	}
	if n := strings.Count(got, key); n != 1 {
		t.Errorf("the dpipe key appears %d times, want 1:\n%s", n, got)
	}

	fi, err := os.Stat(authKeys)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("authorized_keys is %v, want 0600 -- sshd refuses anything looser", fi.Mode().Perm())
	}

	rb, err := os.ReadFile(rootKeys)
	if err != nil {
		t.Fatalf("root did not get the key: %v", err)
	}
	if n := strings.Count(string(rb), key); n != 1 {
		t.Errorf("the dpipe key appears %d times in root's authorized_keys, want 1:\n%s", n, rb)
	}
}

func TestAuthKeyAccountsForAddsTheImageUser(t *testing.T) {
	// dproxy routes a session to the image's user, and an image with its own
	// sshd checks that account's authorized_keys.
	got := authKeyAccountsFor("appuser")
	if len(got) != len(authKeyAccounts)+1 || got[len(got)-1] != "appuser" {
		t.Errorf("authKeyAccountsFor(appuser) = %v, want the defaults plus appuser", got)
	}
	if got := authKeyAccountsFor("appuser:appgroup"); got[len(got)-1] != "appuser" {
		t.Errorf("the group was not stripped: %v", got)
	}
	for _, same := range []string{"", "root", "ubuntu"} {
		if got := authKeyAccountsFor(same); len(got) != len(authKeyAccounts) {
			t.Errorf("authKeyAccountsFor(%q) = %v, want no duplicate", same, got)
		}
	}
}
