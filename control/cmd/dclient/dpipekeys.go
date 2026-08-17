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

// dpipe's two ssh identities have to exist before dpipe starts: with ssh.enabled
// its config names both files, and it will not come up without them. Nothing else
// on the host places them, so a host where dpipe was enabled and nobody ran
// ssh-keygen by hand is a host where the unit restart-loops.
//
// Generated in-process rather than by shelling out to ssh-keygen: the key is
// twelve lines of crypto/ed25519 and x/crypto/ssh, and calling out would make
// openssh a runtime dependency of dclient for something the standard library
// already does. It also lets the private key be written at 0600 from the moment
// it exists, instead of at whatever the caller's umask gave it and fixed up after.

const (
	dpipeKeyDir = "/etc/dpipe/keys"

	// hostKey is what a user's client pins; clientKey is the identity dpipe
	// presents to a guest, and the one whose .pub goes into the guest's
	// authorized_keys. Both names are the ones defaultDpipeConfig points at.
	dpipeHostKeyPath   = dpipeKeyDir + "/dpipe_host_ed25519"
	dpipeClientKeyPath = dpipeKeyDir + "/dpipe_client_ed25519"

	// dpipeCookieSecretPath holds the key proxy verifies login tokens with. Not
	// generated here like the two above: it is the control server's, shared with
	// every host in the fleet, and arrives with the proxy config that names it.
	dpipeCookieSecretPath = dpipeKeyDir + "/cookie_secret"
)

// ensureDpipeKeys creates the key directory and both keypairs if they are not
// already there. Idempotent, and called on every start alongside the rest of the
// dpipe install.
//
// It generates at the default paths, which is where defaultDpipeConfig points.
// An operator who edited dpipe.yaml to name keys somewhere else owns those files
// -- this will still create the defaults, which is harmless, and their config
// keeps using theirs.
func ensureDpipeKeys() error {
	// 0700: the directory holds two private keys and nothing else needs to read
	// it. MkdirAll leaves an existing directory's mode alone, so the Chmod is what
	// repairs a directory some earlier hand-run left group- or world-readable.
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

// ensureEd25519Keypair makes sure path and path+".pub" both exist.
//
// An existing private key is never replaced. Rotating either of these is a
// decision with consequences off this host -- the host key is pinned by clients,
// and the client key is baked into every guest image already built -- so it is an
// operator's to make, by deleting the file.
func ensureEd25519Keypair(path string) error {
	pubPath := path + ".pub"

	priv, err := readEd25519PrivateKey(path)
	switch {
	case err != nil:
		return err
	case priv != nil:
		// The private key is the one that cannot be reconstructed. If its .pub went
		// missing, it is derivable, and deriving it beats refusing to start: without
		// that file no guest image gets dpipe's key installed.
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
	// Private key first: a .pub with no private key beside it would make the next
	// start take the branch above and try to derive from a key that is not there.
	if err := writeNewFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	if err := writePublicKey(pubPath, pub, comment); err != nil {
		return err
	}
	log.Printf("generated %s (ed25519)", path)
	return nil
}

// marshalEd25519PrivateKey encodes the key as an OpenSSH private key block.
//
// Both the value and the pointer are offered because which one
// ssh.MarshalPrivateKey's type switch matches for ed25519 has changed across
// x/crypto releases, and getting it wrong is not a compile error -- it is an
// "unsupported key type" at runtime, on a host, the first time dpipe is enabled.
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

// readEd25519PrivateKey returns nil, nil when the file is not there. An
// unparseable or non-ed25519 key is an error rather than something to overwrite:
// silently replacing a key an operator put there is how a fleet loses access to
// its own guests.
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
	// ParseRawPrivateKey hands back a *ed25519.PrivateKey for this type, not the
	// value, which is a footgun worth naming rather than type-asserting blindly.
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
	// MarshalAuthorizedKey ends in a newline and drops the comment, so the comment
	// is appended in the authorized_keys position ssh-keygen would put it.
	line := fmt.Sprintf("%s %s\n", strings.TrimRight(string(ssh.MarshalAuthorizedKey(signerPub)), "\r\n"), comment)
	return writeNewFile(path, []byte(line), 0o644)
}

// writeNewFile writes through a temporary file in the same directory and renames,
// so a key is never half-written -- and, for the private key, is never readable
// at a looser mode than its final one, since CreateTemp makes it 0600 to begin
// with.
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

// keyComment is what ssh-keygen would have put at the end of the .pub line. It
// ends up in every guest's authorized_keys for the client key, so it says which
// host the key belongs to.
func keyComment(path string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "dclient"
	}
	return filepath.Base(path) + "@" + host
}
