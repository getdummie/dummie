package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/ssh"
)

// userPublicKeyConstraint is the unique index on users.public_key, matched by
// name so a key someone else already holds is distinguished from a clash on
// username or email.
const userPublicKeyConstraint = "users_public_key_key"

// isDuplicatePublicKey reports whether err is the unique violation above.
func isDuplicatePublicKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == userPublicKeyConstraint
}

// maxPublicKeyLen bounds what will be parsed at all. An RSA-4096 key in
// authorized_keys form is around 750 bytes, so this leaves room for a long
// comment and still refuses a pasted file.
const maxPublicKeyLen = 4096

// normalizePublicKey parses an SSH public key in authorized_keys form and
// returns it canonicalised: "<type> <base64>", with the comment and any
// authorized_keys options dropped.
//
// The comment goes because users.public_key is unique: two accounts pasting the
// same key under different comments have to collide, not both be stored.
//
// An empty input is not an error, it is "no key". Deciding what a missing key
// means belongs to the caller: /settings allows clearing one, VM creation does
// not accept the result.
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
	// ParseAuthorizedKey stops at the first key and hands back the remainder, so
	// a paste of a whole authorized_keys file would otherwise be accepted as its
	// first line and silently drop the rest.
	if len(strings.TrimSpace(string(rest))) > 0 {
		return "", errors.New("that is more than one key; a user has one")
	}
	// A private key pasted by mistake fails to parse, but the type check is what
	// catches the weak-but-valid case.
	// "ssh-dss" spelled out rather than ssh.KeyAlgoDSA: the constant is deprecated
	// upstream and the wire name is what is actually being matched.
	if key.Type() == "ssh-dss" {
		return "", errors.New("DSA keys are too weak to accept; use an ed25519 or RSA key")
	}

	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}
