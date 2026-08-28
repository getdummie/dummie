package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"control/internal/proto"
)

// Where the challenge is answered. dproxy owns :80 and forwards
// /.well-known/acme-challenge/ here for the length of an order; nothing is
// listening the rest of the time.
const (
	acmeChallengeHost = "127.0.0.1"
	acmeChallengePort = "8078"
)

const acmeAccountKeyPath = serviceConfigDir + "/acme/account.key"

// One order at a time: the challenge listener is a fixed address, so two
// concurrent orders would fight over it.
var acmeOrderMu sync.Mutex

type acmeAccount struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (a *acmeAccount) GetEmail() string                        { return a.email }
func (a *acmeAccount) GetRegistration() *registration.Resource { return a.reg }
func (a *acmeAccount) GetPrivateKey() crypto.PrivateKey        { return a.key }

func obtainCustomCert(order proto.CustomCertOrder) (string, string, error) {
	domain := strings.ToLower(strings.TrimSpace(order.Domain))
	if domain == "" {
		return "", "", errors.New("the order names no domain")
	}

	acmeOrderMu.Lock()
	defer acmeOrderMu.Unlock()

	key, err := acmeAccountKey()
	if err != nil {
		return "", "", err
	}
	user := &acmeAccount{email: strings.TrimSpace(order.Email), key: key}

	cfg := lego.NewConfig(user)
	cfg.CADirURL = acmeDirectoryURL(order.Directory)
	cfg.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(cfg)
	if err != nil {
		return "", "", fmt.Errorf("could not reach the certificate authority: %w", err)
	}
	if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer(acmeChallengeHost, acmeChallengePort)); err != nil {
		return "", "", fmt.Errorf("could not set up the http challenge: %w", err)
	}

	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return "", "", fmt.Errorf("could not register with the certificate authority: %w", err)
	}
	user.reg = reg

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: []string{domain},
		Bundle:  true,
	})
	if err != nil {
		return "", "", fmt.Errorf("the certificate authority did not issue for %s: %w", domain, err)
	}
	return string(res.Certificate), string(res.PrivateKey), nil
}

func acmeDirectoryURL(s string) string {
	if s == "production" {
		return lego.LEDirectoryProduction
	}
	return lego.LEDirectoryStaging
}

// acmeAccountKey is this host's own, generated on first use and never sent
// anywhere: the control server keeps the certificates, not the account.
func acmeAccountKey() (*ecdsa.PrivateKey, error) {
	if b, err := os.ReadFile(acmeAccountKeyPath); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("%s is not a PEM block", acmeAccountKeyPath)
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("could not read %s: %w", acmeAccountKeyPath, err)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("could not read %s: %w", acmeAccountKeyPath, err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(acmeAccountKeyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create %s: %w", dir, err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := writeFileAtomic(acmeAccountKeyPath, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("could not write %s: %w", acmeAccountKeyPath, err)
	}
	return key, nil
}
