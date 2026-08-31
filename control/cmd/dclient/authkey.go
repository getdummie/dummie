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

const clientPubKeyPath = dpipeClientKeyPath + ".pub"

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
	if strings.ContainsAny(key, "\n\r") {
		return "", fmt.Errorf("%s contains more than one key", clientPubKeyPath)
	}
	return key, nil
}

type passwdUser struct {
	name string
	uid  int
	gid  int
	home string
}

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

// root first: it is the one account every image has, and the guest init's ssh
// server reads its authorized_keys. ubuntu is only served if the image has it.
var authKeyAccounts = []string{"root", "ubuntu"}

func authKeyRecipe() string { return "authkeys=" + strings.Join(authKeyAccounts, ",") }

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
		case os.IsNotExist(err) && u.uid == 0:
		default:
			continue
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none of %s is a usable account in the image's /etc/passwd",
			strings.Join(authKeyAccounts, ", "))
	}
	return out, nil
}

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

func injectAuthorizedKey(root, key string) error {
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

	for _, p := range []string{sshDir, authKeys} {
		if err := os.Chown(p, u.uid, u.gid); err != nil {
			return fmt.Errorf("could not chown %s to %d:%d: %w", p, u.uid, u.gid, err)
		}
	}
	if fi, err := os.Stat(home); err == nil && fi.Mode().Perm()&0o022 != 0 {
		log.Printf("WARNING: %s in the image is group- or world-writable; sshd will refuse the key unless StrictModes is off", u.home)
	}

	log.Printf("added the dpipe key to %s's authorized_keys (uid %d)", u.name, u.uid)
	return nil
}
