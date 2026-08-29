package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4"
)

// The RDP password is derived, never stored. It is recomputable for display from
// the proxy auth secret plus a per-VM nonce, and rotating it means replacing the
// nonce — so a leaked database row still carries no credential, and the generated
// dproxy config carries only the NT hash.
const (
	rdpPasswordLen = 20
	rdpGuestUser   = "ubuntu"
	rdpGuestPass   = "ubuntu"
)

// rdpPassword derives the password a caller types into their RDP client.
func rdpPassword(secret, vmID, nonce string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("rdp:" + vmID + ":" + nonce))
	// Crockford-style base32 without padding: unambiguous to read off a screen
	// and safe to type into a client that may mangle punctuation.
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(mac.Sum(nil))
	return strings.ToLower(enc[:rdpPasswordLen])
}

// rdpNTHash is MD4(UTF16-LE(password)), hex encoded. This is the only form of the
// password that leaves the control server: it is all dproxy needs to verify an
// NTLMv2 response, and it cannot be used to log into anything else.
func rdpNTHash(password string) string {
	u := utf16.Encode([]rune(password))
	b := make([]byte, len(u)*2)
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[i*2:], r)
	}
	h := md4.New()
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
