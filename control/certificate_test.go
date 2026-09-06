package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func testCert(t *testing.T, names []string, notBefore, notAfter time.Time) (certPEM, keyPEM string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("could not generate a key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("could not create the certificate: %v", err)
	}
	der8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("could not marshal the key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der8}))
}

func testFleetCert(t *testing.T, tld string) (string, string) {
	t.Helper()
	return testCert(t, certificateNames(tld), time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
}

func TestCertificateNamesCoverConsolesAndIntegrations(t *testing.T) {
	got := certificateNames("example.com")
	want := []string{"*.example.com", "*.shell.example.com", "*.int.example.com"}
	if len(got) != len(want) {
		t.Fatalf("certificateNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("certificateNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCertificateRequiredNamesCoverConsoles(t *testing.T) {
	got := certificateRequiredNames("example.com")
	want := []string{"*.example.com", "*.shell.example.com"}
	if len(got) != len(want) {
		t.Fatalf("certificateRequiredNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("certificateRequiredNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Adding *.int.<tld> to what we order must not invalidate a certificate issued
// before it existed, or every fleet on an uploaded or manually-issued
// certificate would be unable to renew or re-upload until it reissued.
func TestValidateCertificateAcceptsACertIssuedBeforeIntegrations(t *testing.T) {
	certPEM, keyPEM := testCert(t, certificateRequiredNames("example.com"),
		time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))

	if _, err := validateCertificate(certPEM, keyPEM, "example.com"); err != nil {
		t.Fatalf("a certificate predating the integration names was rejected: %v", err)
	}
}

func TestCertificateCoversIntegrations(t *testing.T) {
	if certificateCoversIntegrations(certificateRequiredNames("example.com"), "example.com") {
		t.Error("a certificate without the integration wildcard was reported as covering it")
	}
	if !certificateCoversIntegrations(certificateNames("example.com"), "example.com") {
		t.Error("a certificate with the integration wildcard was not recognised")
	}
}

func TestCertificateNamesOmitTheApex(t *testing.T) {
	for _, n := range certificateNames("example.com") {
		if n == "example.com" {
			t.Error("the apex is requested; its challenge collides with the wildcard's")
		}
	}
}

func TestValidateCertificateAcceptsAnApexItDidNotAskFor(t *testing.T) {
	names := append([]string{"example.com"}, certificateNames("example.com")...)
	certPEM, keyPEM := testCert(t, names, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))

	if _, err := validateCertificate(certPEM, keyPEM, "example.com"); err != nil {
		t.Fatalf("a certificate carrying the apex as well was rejected: %v", err)
	}
}

func TestValidateCertificateAcceptsAFleetCertificate(t *testing.T) {
	certPEM, keyPEM := testFleetCert(t, "example.com")

	info, err := validateCertificate(certPEM, keyPEM, "example.com")
	if err != nil {
		t.Fatalf("a certificate cut for this fleet was rejected: %v", err)
	}
	if len(info.Fingerprint) != 64 {
		t.Errorf("fingerprint = %q, want a 64-character sha256", info.Fingerprint)
	}
	if info.NotAfter.Before(time.Now()) {
		t.Errorf("NotAfter = %v, want a future time", info.NotAfter)
	}
}

func TestValidateCertificateRejectsMissingConsoleWildcard(t *testing.T) {
	certPEM, keyPEM := testCert(t,
		[]string{"example.com", "*.example.com"},
		time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))

	_, err := validateCertificate(certPEM, keyPEM, "example.com")
	if err == nil {
		t.Fatal("a certificate with no console wildcard was accepted")
	}
	if !strings.Contains(err.Error(), "*.shell.example.com") {
		t.Errorf("the error does not name the missing name: %v", err)
	}
}

func TestValidateCertificateRejectsAnotherDomain(t *testing.T) {
	certPEM, keyPEM := testFleetCert(t, "other.example")

	if _, err := validateCertificate(certPEM, keyPEM, "example.com"); err == nil {
		t.Fatal("a certificate for a different domain was accepted")
	}
}

func TestValidateCertificateRejectsAMismatchedKey(t *testing.T) {
	certPEM, _ := testFleetCert(t, "example.com")
	_, otherKey := testFleetCert(t, "example.com")

	_, err := validateCertificate(certPEM, otherKey, "example.com")
	if err == nil {
		t.Fatal("a certificate was accepted with someone else's private key")
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Errorf("the error quotes the private key: %v", err)
	}
}

func TestValidateCertificateRejectsExpired(t *testing.T) {
	certPEM, keyPEM := testCert(t, certificateNames("example.com"),
		time.Now().Add(-90*24*time.Hour), time.Now().Add(-time.Hour))

	_, err := validateCertificate(certPEM, keyPEM, "example.com")
	if err == nil {
		t.Fatal("an expired certificate was accepted")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCertificateRejectsNotYetValid(t *testing.T) {
	certPEM, keyPEM := testCert(t, certificateNames("example.com"),
		time.Now().Add(time.Hour), time.Now().Add(90*24*time.Hour))

	if _, err := validateCertificate(certPEM, keyPEM, "example.com"); err == nil {
		t.Fatal("a certificate that is not valid yet was accepted")
	}
}

func TestValidateCertificateRejectsEmpty(t *testing.T) {
	certPEM, keyPEM := testFleetCert(t, "example.com")

	if _, err := validateCertificate("", keyPEM, "example.com"); err == nil {
		t.Error("an empty certificate was accepted")
	}
	if _, err := validateCertificate(certPEM, "", "example.com"); err == nil {
		t.Error("an empty private key was accepted")
	}
}

func TestNormalizePEMStripsNoiseAroundTheBlocks(t *testing.T) {
	certPEM, _ := testFleetCert(t, "example.com")
	pasted := "Bag Attributes\n    friendlyName: example\n" +
		strings.ReplaceAll("  \n"+certPEM+"\n  ", "\n", "\r\n")

	got, err := normalizePEM(pasted)
	if err != nil {
		t.Fatalf("a realistic paste was rejected: %v", err)
	}
	if got != certPEM {
		t.Errorf("normalizePEM did not recover the original block:\n%q\nwant\n%q", got, certPEM)
	}
	if _, err := validateCertificate(got, "", "example.com"); err == nil {
		t.Error("expected the empty key to still be rejected after normalizing")
	}
}

func TestNormalizePEMKeepsEveryBlock(t *testing.T) {
	leaf, _ := testFleetCert(t, "example.com")
	intermediate, _ := testFleetCert(t, "ca.example.com")

	got, err := normalizePEM(leaf + intermediate)
	if err != nil {
		t.Fatalf("a chain was rejected: %v", err)
	}
	if n := strings.Count(got, "-----BEGIN CERTIFICATE-----"); n != 2 {
		t.Errorf("the normalized chain has %d certificates, want 2", n)
	}
}

func TestNormalizePEMRejectsSomethingElse(t *testing.T) {
	if _, err := normalizePEM("this is not a certificate"); err == nil {
		t.Error("a non-PEM value was accepted")
	}
}
