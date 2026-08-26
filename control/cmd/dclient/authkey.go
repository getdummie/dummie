package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// dpipe reaches into guests over ssh, which means its public key has to already
// be in the guest before the guest has ever been touched. There is no cloud-init
// and no guest client in these images, so the key is written into the root
// filesystem while it is still a directory on the host -- after the tar is
// extracted and before mkfs turns it into an image.
//
// This is create-time only, and only for tar-built rootfs images. A VM whose
// disk already exists keeps whatever its disk has; rotating the key reaches it
// by rebuilding, not by editing a running guest.

// clientPubKeyPath is the key dpipe authenticates *as* when it connects out to a
// guest -- its client identity, not the host key a guest would use to identify
// itself. That is the one that belongs in a guest's authorized_keys.
//
// The same on every host, so it is a constant rather than config: dclient places
// it itself when dpipe is enabled (see ensureDpipeKeys), and a host where it
// differs is a host where dpipe is not what we think it is.
const clientPubKeyPath = dpipeClientKeyPath + ".pub"

// readClientPubKey returns the key, or "" if there is none.
//
// Missing is not an error: a host that does not run dpipe has no key and should
// still be able to build images. Unreadable *is* an error -- a key that exists
// but cannot be read is a broken install, and silently building a guest nobody
// can log into would hide it.
func readClientPubKey() (string, error) {
	b, err := os.ReadFile(clientPubKeyPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read %s: %w", clientPubKeyPath, err)
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", fmt.Errorf("%s is empty", clientPubKeyPath)
	}
	// One key, one line. A file that grew a second line would otherwise be
	// written into authorized_keys as-is, granting access nobody reviewed.
	if strings.ContainsAny(key, "\n\r") {
		return "", fmt.Errorf("%s contains more than one key", clientPubKeyPath)
	}
	return key, nil
}

// passwdUser is the part of an /etc/passwd line this needs.
type passwdUser struct {
	name string
	uid  int
	gid  int
	home string
}

// lookupPasswdUser reads the *image's* /etc/passwd, not the host's. The uid the
// guest knows a user by is a fact about the guest, and assuming 1000 because
// that is what Ubuntu usually does would produce a file owned by the wrong user
// on any image where it is not -- which sshd answers by ignoring the key.
func lookupPasswdUser(root, name string) (passwdUser, bool) {
	b, err := os.ReadFile(filepath.Join(root, "etc", "passwd"))
	if err != nil {
		return passwdUser{}, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 6 || f[0] != name {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		gid, err := strconv.Atoi(f[3])
		if err != nil {
			continue
		}
		return passwdUser{name: name, uid: uid, gid: gid, home: f[5]}, true
	}
	return passwdUser{}, false
}

// authKeyAccounts is who the key goes to, in the order they are tried. Both, not
// one: an image that brings its own ubuntu without sudo would otherwise leave
// dpipe unable to reach anything but that account.
var authKeyAccounts = []string{"ubuntu", "root"}

// authKeyRecipe is folded into a built image's cache identity so that editing
// authKeyAccounts rebuilds rather than serving the image built under the old
// list, which the tar and the key alone cannot tell apart.
func authKeyRecipe() string { return "authkeys=" + strings.Join(authKeyAccounts, ",") }

// authKeyTargets resolves authKeyAccounts against the image's own /etc/passwd,
// skipping the ones it does not have.
//
// An account counts only with a passwd entry *and* a home directory that is
// really a directory. A minimal image can carry an ubuntu entry pointing at a
// home that was never created, and creating it here would produce an account
// that looks usable and is not. Root is the one exception, and only for a home
// that is absent rather than odd: uid 0 owns whatever is created for it.
func authKeyTargets(root string) ([]passwdUser, error) {
	var out []passwdUser
	for _, name := range authKeyAccounts {
		u, ok := lookupPasswdUser(root, name)
		if !ok {
			continue
		}
		home, err := imagePath(root, u.home)
		if err != nil {
			continue
		}
		fi, err := os.Lstat(home)
		switch {
		case err == nil && fi.IsDir():
		// A home that is not there at all is made below, but only for uid 0,
		// which owns whatever is created for it.
		case os.IsNotExist(err) && u.uid == 0:
		// Anything else -- a symlink most of all, since the tar is operator
		// supplied and this runs as root on the host.
		default:
			continue
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		// No passwd at all, or one with neither account usable: not a filesystem
		// anything will boot from, so guessing at uid 0 and /root would be
		// inventing an answer.
		return nil, fmt.Errorf("none of %s is a usable account in the image's /etc/passwd",
			strings.Join(authKeyAccounts, ", "))
	}
	return out, nil
}

// imagePath resolves a guest-absolute path inside the extracted image and
// refuses one that climbs out of it. The tar is an operator-supplied artifact
// unpacked as root, so a home directory of "/../../etc" would otherwise have
// this writing to the host's own filesystem.
func imagePath(root, guestPath string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, filepath.Clean("/"+guestPath))
	if p != root && !strings.HasPrefix(p, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the image root", guestPath)
	}
	return p, nil
}

// injectAuthorizedKey adds dpipe's client key to every account of
// authKeyAccounts the image rooted at root has.
//
// All of them rather than the first: an image that brings its own ubuntu without
// sudo leaves root the only way to configure anything, and an image with no
// ubuntu at all leaves root the only way in.
//
// Appended, never written over: an image that ships its own authorized_keys put
// them there on purpose, and replacing the file would lock out whoever was
// relying on them. Re-adding a key that is already present is skipped, so
// rebuilding the same image twice does not accumulate duplicates.
func injectAuthorizedKey(root, key string) error {
	// Checked up front for the message. Without root the chown below fails and
	// sshd ignores a key file owned by the wrong user, so the image would build
	// and boot and simply not let dpipe in -- and the cache would then hand that
	// image to every later create.
	if os.Geteuid() != 0 {
		return errors.New("installing the dpipe key needs root, so that authorized_keys ends up owned by the guest user")
	}

	targets, err := authKeyTargets(root)
	if err != nil {
		return err
	}
	for _, u := range targets {
		if err := installAuthorizedKey(root, key, u); err != nil {
			return fmt.Errorf("%s: %w", u.name, err)
		}
	}
	return nil
}

// installAuthorizedKey adds the key to one account in the image.
func installAuthorizedKey(root, key string, u passwdUser) error {
	home, err := imagePath(root, u.home)
	if err != nil {
		return err
	}

	sshDir := filepath.Join(home, ".ssh")
	authKeys := filepath.Join(sshDir, "authorized_keys")

	existing, err := os.ReadFile(authKeys)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == key {
			log.Printf("the dpipe key is already in %s's authorized_keys", u.name)
			return nil
		}
	}

	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	body := string(existing)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += key + "\n"
	if err := os.WriteFile(authKeys, []byte(body), 0o600); err != nil {
		return err
	}

	// sshd ignores an authorized_keys it does not trust the ownership of, and
	// MkdirAll leaves both owned by whoever ran this -- root on the host, which is
	// only the right answer when the target happens to be root.
	for _, p := range []string{sshDir, authKeys} {
		if err := os.Chown(p, u.uid, u.gid); err != nil {
			return fmt.Errorf("could not chown %s to %d:%d: %w", p, u.uid, u.gid, err)
		}
	}
	// Ownership of ~ matters too: sshd rejects a home directory owned by someone
	// other than the user unless StrictModes is off. Only fixed if it is wrong,
	// since an image that set it deliberately should keep what it set.
	if fi, err := os.Stat(home); err == nil && fi.Mode().Perm()&0o022 != 0 {
		log.Printf("WARNING: %s in the image is group- or world-writable; sshd will refuse the key unless StrictModes is off", u.home)
	}

	log.Printf("added the dpipe key to %s's authorized_keys (uid %d)", u.name, u.uid)
	return nil
}
