package dpipe

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// The RDP client leg needs a certificate before NLA can start, and RDP clients
// send no SNI, so the per-name certs cannot be selected. The fleet's own
// certificate is used when there is one; a fleet with no TLS at all falls back to
// a self-signed certificate generated here.
//
// The fallback does not weaken the credential check: CredSSP binds each leg to
// that leg's own public key, so a substituted proxy fails the binding regardless
// of who signed the certificate. What it does cost is server authentication —
// a client cannot tell this proxy from an impostor before it types a password,
// and will show an "unknown publisher" prompt. The guest's own
// gnome-remote-desktop makes the same trade with the same kind of certificate.
const (
	rdpSelfSignedCert = "/etc/dpipe/certs/rdp-self-signed.pem"
	rdpSelfSignedKey  = "/etc/dpipe/certs/rdp-self-signed.key"

	rdpSelfSignedLifetime = 10 * 365 * 24 * time.Hour
)

// loadRDPCert returns the certificate the RDP client leg presents, generating and
// persisting a self-signed one on first use when the fleet has no default cert.
// It is persisted so a dpipe restart does not re-prompt every client.
func loadRDPCert(c *Config, def *tls.Certificate, computerName string) (*tls.Certificate, error) {
	if def != nil {
		return def, nil
	}

	certPath, keyPath := c.RDP.selfSignedPaths()
	if crt, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return &crt, nil
	}

	crt, certPEM, keyPEM, err := generateSelfSigned(computerName)
	if err != nil {
		return nil, err
	}
	// A host that cannot persist it still gets a working desktop; it just hands
	// out a new certificate after every restart.
	if err := writeSelfSigned(certPath, keyPath, certPEM, keyPEM); err != nil {
		return crt, err
	}
	return crt, nil
}

func (c RDPConfig) selfSignedPaths() (string, string) {
	cert, key := c.SelfSignedCert, c.SelfSignedKey
	if cert == "" {
		cert = rdpSelfSignedCert
	}
	if key == "" {
		key = rdpSelfSignedKey
	}
	return cert, key
}

func generateSelfSigned(computerName string) (*tls.Certificate, []byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rdp self-signed key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rdp self-signed serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: computerName},
		DNSNames:              []string{computerName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(rdpSelfSignedLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rdp self-signed certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	crt := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustMarshalKey(key)})
	return crt, certPEM, keyPEM, nil
}

func mustMarshalKey(key *rsa.PrivateKey) []byte {
	b, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		// An RSA key always marshals; a failure here would be a runtime bug, not
		// a configuration problem.
		panic(err)
	}
	return b
}

func writeSelfSigned(certPath, keyPath string, certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return fmt.Errorf("rdp self-signed: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("rdp self-signed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return fmt.Errorf("rdp self-signed: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("rdp self-signed: %w", err)
	}
	return nil
}
