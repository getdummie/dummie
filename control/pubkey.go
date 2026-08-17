package main

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// maxPublicKeyLen bounds what will be parsed at all. An RSA-4096 key in
// authorized_keys form is around 750 bytes, so this leaves room for a long
// comment and still refuses a pasted file.
const maxPublicKeyLen = 4096

// normalizePublicKey parses an SSH public key in authorized_keys form and
// returns it canonicalised: "<type> <base64> <comment>".
//
// Canonicalised rather than stored as typed, for two reasons. The base64 is
// re-encoded from the parsed key, so whitespace and line-wrapping variations of
// the same key are one stored value rather than several. And authorized_keys
// options -- command=, environment=, permitopen= and friends -- are dropped:
// they are instructions to sshd, and a user-supplied string that changes what
// happens when a key is used is not something this server should carry into a
// guest unreviewed.
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

	key, comment, _, rest, err := ssh.ParseAuthorizedKey([]byte(s))
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

	// MarshalAuthorizedKey returns "<type> <base64>\n" and drops the comment, so
	// the comment is re-attached rather than kept from the input verbatim.
	out := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	// The comment is free text from the client and this line is destined for an
	// authorized_keys file, where a newline starts a new key. Parsing is
	// line-oriented so one should never reach here -- dropping it rather than
	// trusting that is a one-line guarantee instead of an assumption.
	if comment = strings.TrimSpace(comment); comment != "" && !strings.ContainsAny(comment, "\r\n") {
		out += " " + comment
	}
	return out, nil
}
