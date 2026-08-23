package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A fleet's certificate has to carry two wildcards, not one.
//
// Guests are published at "<vm>.<domain>" and their browser terminals at
// "<vm>.shell.<domain>". A wildcard matches exactly one label, so "*.<domain>"
// covers the first and not the second -- an omission that shows up only when
// someone opens a console, which is the worst moment to find it. Both names are
// required here, of every certificate, however it was obtained.
//
// This is the one validation in the feature that has to be strict: a certificate
// that gets past it becomes a dpipe.yaml with tls enabled, and a host that cannot
// load what that file names does not start at all.

// certInfo is what the domains table records about a certificate: enough to show
// it, compare it and decide when to replace it, without reading the bucket.
type certInfo struct {
	Fingerprint string
	NotAfter    time.Time
	NotBefore   time.Time
	DNSNames    []string
}

// validateCertificate checks that a PEM chain and key are usable as the
// certificate for tld, and returns what should be recorded about them.
//
// The chain is taken whole: the leaf must be first, which is what every ca and
// every acme client emits, and the rest travels with it because dpipe serves
// exactly the bytes it is given and a missing intermediate is a certificate that
// verifies in curl and fails in a browser.
func validateCertificate(certPEM, keyPEM, tld string) (certInfo, error) {
	if strings.TrimSpace(certPEM) == "" {
		return certInfo{}, errors.New("the certificate is empty")
	}
	if strings.TrimSpace(keyPEM) == "" {
		return certInfo{}, errors.New("the private key is empty")
	}

	// X509KeyPair is the pairing check: it parses both and confirms the public key
	// in the leaf is the one the private key belongs to. Doing it any other way
	// would mean re-implementing that comparison per key algorithm.
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		// Deliberately not wrapped with the input: a parse error on a private key
		// should not be able to put any of it in a log line or an api response.
		return certInfo{}, fmt.Errorf("the certificate and private key are not a usable pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return certInfo{}, errors.New("the certificate file contains no certificate")
	}

	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return certInfo{}, fmt.Errorf("could not read the certificate: %w", err)
	}

	now := time.Now()
	if now.After(leaf.NotAfter) {
		return certInfo{}, fmt.Errorf("the certificate expired on %s", leaf.NotAfter.Format(time.RFC3339))
	}
	// Rejected rather than accepted-and-warned: installing it would give the fleet
	// a certificate no browser accepts yet, and the failure would look like a
	// broken deployment rather than a clock.
	if now.Before(leaf.NotBefore) {
		return certInfo{}, fmt.Errorf("the certificate is not valid until %s", leaf.NotBefore.Format(time.RFC3339))
	}

	for _, name := range certificateNames(tld) {
		// VerifyHostname rather than a scan of DNSNames: wildcard matching has rules
		// (one label, leftmost only, no partial labels) and this is the
		// implementation the clients connecting to these hosts will use.
		if err := leaf.VerifyHostname(name); err != nil {
			return certInfo{}, fmt.Errorf("the certificate does not cover %s, which this fleet serves", name)
		}
	}

	sum := sha256.Sum256(pair.Certificate[0])
	return certInfo{
		Fingerprint: hex.EncodeToString(sum[:]),
		NotAfter:    leaf.NotAfter,
		NotBefore:   leaf.NotBefore,
		DNSNames:    leaf.DNSNames,
	}, nil
}

// certificateNames is what a certificate for tld has to cover, and what an acme
// order for it asks for. One list, so the check and the request cannot drift.
//
// Two names, and deliberately not the apex as well. Nothing this fleet serves
// lives at the bare domain -- guests are "<vm>.<tld>" and their terminals are
// "<vm>.shell.<tld>" -- so asking for it buys nothing, and it costs something
// real: the dns-01 challenge for "<tld>" and the one for "*.<tld>" are published
// at the same record name with different values, which reads as a contradiction
// to anyone creating them by hand and is easy to satisfy by replacing one with
// the other. Both authorizations then fail.
//
// A certificate that happens to carry the apex anyway is still accepted; this is
// the minimum, not the exact set.
func certificateNames(tld string) []string {
	return []string{
		"*." + tld,
		"*." + proxyConsoleLabel + "." + tld,
	}
}

// normalizePEM makes an operator-pasted block safe to store and compare: CRLFs
// out, surrounding whitespace off, exactly one trailing newline.
//
// It re-encodes rather than trimming, which also drops anything between the
// blocks -- the "Bag Attributes" preamble some tools emit, and any comment that
// came along with a copy and paste.
func normalizePEM(s string) (string, error) {
	rest := []byte(strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n"))
	var out strings.Builder
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if err := pem.Encode(&out, block); err != nil {
			return "", err
		}
	}
	if out.Len() == 0 {
		return "", errors.New("no PEM block found")
	}
	return out.String(), nil
}
