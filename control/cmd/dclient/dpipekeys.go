package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	dpipeKeyDir = "/etc/dpipe/keys"

	dpipeHostKeyPath   = dpipeKeyDir + "/dpipe_host_ed25519"
	dpipeClientKeyPath = dpipeKeyDir + "/dpipe_client_ed25519"

	dpipeCookieSecretPath = dpipeKeyDir + "/cookie_secret"

	dpipeCertDir  = "/etc/dpipe/certs"
	dpipeCertPath = dpipeCertDir + "/fullchain.pem"
	dpipeKeyPath  = dpipeCertDir + "/privkey.pem"
)

func ensureDpipeKeys() error {
	if err := os.MkdirAll(dpipeKeyDir, 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", dpipeKeyDir, err)
	}
	if err := os.Chmod(dpipeKeyDir, 0o700); err != nil {
		return fmt.Errorf("could not tighten %s to 0700: %w", dpipeKeyDir, err)
	}
	for _, path := range []string{dpipeHostKeyPath, dpipeClientKeyPath} {
		if err := ensureEd25519Keypair(path); err != nil {
			return err
		}
	}
	return nil
}

func ensureEd25519Keypair(path string) error {
	pubPath := path + ".pub"

	priv, err := readEd25519PrivateKey(path)
	switch {
	case err != nil:
		return err
	case priv != nil:
		if _, err := os.Stat(pubPath); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		log.Printf("%s is missing; deriving it from %s", pubPath, path)
		return writePublicKey(pubPath, priv.Public().(ed25519.PublicKey), keyComment(path))
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("could not generate an ed25519 key: %w", err)
	}
	comment := keyComment(path)

	block, err := marshalEd25519PrivateKey(priv, comment)
	if err != nil {
		return fmt.Errorf("could not encode %s: %w", path, err)
	}
	if err := writeNewFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	if err := writePublicKey(pubPath, pub, comment); err != nil {
		return err
	}
	log.Printf("generated %s (ed25519)", path)
	return nil
}

func marshalEd25519PrivateKey(priv ed25519.PrivateKey, comment string) (*pem.Block, error) {
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err == nil {
		return block, nil
	}
	if block, ptrErr := ssh.MarshalPrivateKey(&priv, comment); ptrErr == nil {
		return block, nil
	}
	return nil, err
}

func readEd25519PrivateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	key, err := ssh.ParseRawPrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("%s is not a usable private key (%w); move it aside to have a new one generated", path, err)
	}
	switch k := key.(type) {
	case *ed25519.PrivateKey:
		return *k, nil
	case ed25519.PrivateKey:
		return k, nil
	default:
		return nil, fmt.Errorf("%s is a %T, not an ed25519 key; move it aside to have a new one generated", path, key)
	}
}

func writePublicKey(path string, pub ed25519.PublicKey, comment string) error {
	signerPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return fmt.Errorf("could not encode %s: %w", path, err)
	}
	line := fmt.Sprintf("%s %s\n", strings.TrimRight(string(ssh.MarshalAuthorizedKey(signerPub)), "\r\n"), comment)
	return writeNewFile(path, []byte(line), 0o644)
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

func keyComment(path string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "dclient"
	}
	return filepath.Base(path) + "@" + host
}
