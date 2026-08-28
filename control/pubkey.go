package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/ssh"
)

const userPublicKeyConstraint = "users_public_key_key"

func isDuplicatePublicKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == userPublicKeyConstraint
}

const maxPublicKeyLen = 4096

func normalizePublicKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > maxPublicKeyLen {
		return "", fmt.Errorf("that key is longer than %d characters; paste one public key, not a file", maxPublicKeyLen)
	}

	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return "", errors.New("that does not look like an SSH public key; paste the contents of a .pub file, e.g. ssh-ed25519 AAAA… you@host")
	}
	if len(strings.TrimSpace(string(rest))) > 0 {
		return "", errors.New("that is more than one key; a user has one")
	}
	if key.Type() == "ssh-dss" {
		return "", errors.New("DSA keys are too weak to accept; use an ed25519 or RSA key")
	}

	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}
