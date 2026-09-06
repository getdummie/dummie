package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"control/internal/proto"
)

const (
	dpipeKeyDir = "/etc/dpipe/keys"

	dpipeHostKeyName   = "dpipe_host_ed25519"
	dpipeClientKeyName = "dpipe_client_ed25519"

	dpipeHostKeyPath   = dpipeKeyDir + "/" + dpipeHostKeyName
	dpipeClientKeyPath = dpipeKeyDir + "/" + dpipeClientKeyName

	dpipeCookieSecretPath = dpipeKeyDir + "/cookie_secret"

	dpipeCertDir  = "/etc/dpipe/certs"
	dpipeCertPath = dpipeCertDir + "/fullchain.pem"
	dpipeKeyPath  = dpipeCertDir + "/privkey.pem"

	dpipeNamedCertDir = dpipeCertDir + "/named"
)

// writeDpipeKeys installs the ssh identities the control server issued. The
// host never generates its own: the whole fleet serves one host key, so a
// rebuilt host is the same host as far as a user's ssh client is concerned.
func writeDpipeKeys(keys *proto.DpipeSSHKeys) (bool, error) {
	return writeDpipeKeysTo(dpipeKeyDir, keys)
}

func writeDpipeKeysTo(dir string, keys *proto.DpipeSSHKeys) (bool, error) {
	if keys == nil {
		return false, nil
	}
	if keys.HostKey == "" || keys.ClientKey == "" {
		return false, errors.New("the control server sent an incomplete set of dpipe ssh keys")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("could not create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return false, fmt.Errorf("could not tighten %s to 0700: %w", dir, err)
	}

	changed := false
	for _, k := range []struct {
		path, key, pub string
	}{
		{filepath.Join(dir, dpipeHostKeyName), keys.HostKey, keys.HostPub},
		{filepath.Join(dir, dpipeClientKeyName), keys.ClientKey, keys.ClientPub},
	} {
		keyChanged, err := writeIfChanged(k.path, k.key, 0o600)
		if err != nil {
			return changed, err
		}
		pubChanged := false
		if k.pub != "" {
			pubChanged, err = writeIfChanged(k.path+".pub", k.pub, 0o644)
			if err != nil {
				return changed, err
			}
		}
		if keyChanged || pubChanged {
			log.Printf("installed the ssh key the control server issued in %s", k.path)
			changed = true
		}
	}
	return changed, nil
}

func dpipeKeysPresent() bool {
	for _, p := range []string{dpipeHostKeyPath, dpipeClientKeyPath} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

func writeNewFile(path string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dclient-key-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
