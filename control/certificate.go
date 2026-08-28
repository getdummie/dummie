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

type certInfo struct {
	Fingerprint string
	NotAfter    time.Time
	NotBefore   time.Time
	DNSNames    []string
}

func validateCertificate(certPEM, keyPEM, tld string) (certInfo, error) {
	return validateCertificateFor(certPEM, keyPEM, certificateNames(tld)...)
}

func validateCertificateFor(certPEM, keyPEM string, names ...string) (certInfo, error) {
	if strings.TrimSpace(certPEM) == "" {
		return certInfo{}, errors.New("the certificate is empty")
	}
	if strings.TrimSpace(keyPEM) == "" {
		return certInfo{}, errors.New("the private key is empty")
	}

	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
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
	if now.Before(leaf.NotBefore) {
		return certInfo{}, fmt.Errorf("the certificate is not valid until %s", leaf.NotBefore.Format(time.RFC3339))
	}

	for _, name := range names {
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

func certificateNames(tld string) []string {
	return []string{
		"*." + tld,
		"*." + proxyConsoleLabel + "." + tld,
	}
}

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
