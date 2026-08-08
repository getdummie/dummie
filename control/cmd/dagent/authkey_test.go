package main

import (
  "os"
  "path/filepath"
  "strings"
  "testing"
)

// fakeRoot builds a directory that looks enough like an extracted rootfs.
// homes are guest-absolute paths to create.
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

func TestAuthKeyTarget(t *testing.T) {
  for _, tc := range []struct {
    name     string
    passwd   string
    homes    []string
    wantUser string
    wantUID  int
  }{
    {"ubuntu wins when it is there", ubuntuPasswd,
      []string{"root", "home/ubuntu"}, "ubuntu", 1000},
    {"root when there is no ubuntu", "root:x:0:0:root:/root:/bin/bash\n",
      []string{"root"}, "root", 0},
    // A passwd entry whose home was never created is an account that looks
    // usable and is not; root is the more useful answer.
    {"root when ubuntu has no home", ubuntuPasswd,
      []string{"root"}, "root", 0},
    // Not every image gives ubuntu 1000; assuming it would chown to the wrong
    // user, which sshd answers by ignoring the key.
    {"uid is read from the image", "root:x:0:0:root:/root:/bin/sh\nubuntu:x:1500:1600:U:/home/ubuntu:/bin/sh\n",
      []string{"root", "home/ubuntu"}, "ubuntu", 1500},
  } {
    t.Run(tc.name, func(t *testing.T) {
      got, err := authKeyTarget(fakeRoot(t, tc.passwd, tc.homes...))
      if err != nil {
        t.Fatalf("authKeyTarget: %v", err)
      }
      if got.name != tc.wantUser || got.uid != tc.wantUID {
        t.Errorf("got %s (uid %d), want %s (uid %d)", got.name, got.uid, tc.wantUser, tc.wantUID)
      }
    })
  }
}

func TestAuthKeyTargetNeedsAPasswd(t *testing.T) {
  if _, err := authKeyTarget(t.TempDir()); err == nil {
    t.Error("expected an error for an image with no /etc/passwd")
  }
}

// The tar is an operator-supplied artifact unpacked as root. A home directory
// that climbs out of the image would have this writing to the host.
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

// The same tar and the same key must land on the same cache entry, or every
// create rebuilds; a different key must not, or a rotated key never reaches a
// guest.
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

func TestInjectAuthorizedKeyAppends(t *testing.T) {
  if os.Geteuid() != 0 {
    t.Skip("injectAuthorizedKey chowns, which needs root")
  }
  root := fakeRoot(t, ubuntuPasswd, "root", "home/ubuntu")
  authKeys := filepath.Join(root, "home", "ubuntu", ".ssh", "authorized_keys")

  // An image that ships its own keys put them there on purpose.
  if err := os.MkdirAll(filepath.Dir(authKeys), 0o700); err != nil {
    t.Fatal(err)
  }
  if err := os.WriteFile(authKeys, []byte("ssh-rsa AAAA theirs\n"), 0o600); err != nil {
    t.Fatal(err)
  }

  const key = "ssh-ed25519 AAAA dpipe"
  if err := injectAuthorizedKey(root, key); err != nil {
    t.Fatal(err)
  }
  // Twice, because a rebuild must not accumulate duplicates.
  if err := injectAuthorizedKey(root, key); err != nil {
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
}
