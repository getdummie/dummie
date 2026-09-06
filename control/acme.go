package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
)

const (
	certModeUpload = "upload"
	certModeACMEManual = "acme_manual"
	certModeACMECloudflare = "acme_cloudflare"
)

const (
	acmeDirectoryStaging    = "staging"
	acmeDirectoryProduction = "production"
)

const acmeChallengeTimeout = 30 * time.Minute

const certRenewBefore = 30 * 24 * time.Hour

func validCertMode(s string) bool {
	switch s {
	case certModeUpload, certModeACMEManual, certModeACMECloudflare:
		return true
	}
	return false
}

func directoryURL(s string) string {
	if s == acmeDirectoryProduction {
		return lego.LEDirectoryProduction
	}
	return lego.LEDirectoryStaging
}

type dnsRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

const (
	stageStarting   = "contacting the certificate authority"
	stageWaiting    = "waiting for the dns records to be created"
	stageValidating = "checking the dns records and validating with the ca"
	stageStoring    = "storing the certificate and sending it to the hosts"
)

type pendingOrder struct {
	mu      sync.Mutex
	records []dnsRecord
	stage   string

	proceed chan struct{}
	cancel  chan struct{}

	confirmOnce sync.Once
	cancelOnce  sync.Once
}

func (p *pendingOrder) confirm() { p.confirmOnce.Do(func() { close(p.proceed) }) }
func (p *pendingOrder) abandon() { p.cancelOnce.Do(func() { close(p.cancel) }) }

func (p *pendingOrder) addRecord(r dnsRecord) {
	p.mu.Lock()
	p.records = append(p.records, r)
	p.mu.Unlock()
}

func (p *pendingOrder) clearRecords() {
	p.mu.Lock()
	p.records = nil
	p.mu.Unlock()
}

func (p *pendingOrder) snapshot() ([]dnsRecord, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]dnsRecord(nil), p.records...), p.stage
}

func (p *pendingOrder) setStage(s string) {
	p.mu.Lock()
	p.stage = s
	p.mu.Unlock()
}

type certIssuer struct {
	q     *db.Queries
	blobs *blobStore
	hub   *Hub
	proxy proxyAuthConfig

	mu sync.Mutex
	inFlight map[string]*pendingOrder
}

func newCertIssuer(q *db.Queries, blobs *blobStore, hub *Hub, proxy proxyAuthConfig) *certIssuer {
	return &certIssuer{q: q, blobs: blobs, hub: hub, proxy: proxy, inFlight: map[string]*pendingOrder{}}
}

func (ci *certIssuer) Pending(domainID pgtype.UUID) ([]dnsRecord, string, bool) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	p, ok := ci.inFlight[uuid.UUID(domainID.Bytes).String()]
	if !ok {
		return nil, "", false
	}
	records, stage := p.snapshot()
	return records, stage, true
}

func (ci *certIssuer) Confirm(domainID pgtype.UUID) error {
	ci.mu.Lock()
	p, ok := ci.inFlight[uuid.UUID(domainID.Bytes).String()]
	ci.mu.Unlock()
	if !ok {
		return errors.New("there is no certificate order waiting for this domain")
	}
	if records, _ := p.snapshot(); len(records) == 0 {
		return errors.New("the order has not published its challenge yet")
	}
	p.confirm()
	return nil
}

func (ci *certIssuer) Cancel(domainID pgtype.UUID) error {
	ci.mu.Lock()
	p, ok := ci.inFlight[uuid.UUID(domainID.Bytes).String()]
	ci.mu.Unlock()
	if !ok {
		return errors.New("there is no certificate order running for this domain")
	}
	p.abandon()
	return nil
}

func (ci *certIssuer) Start(domain db.Domain) error {
	id := uuid.UUID(domain.ID.Bytes).String()

	ci.mu.Lock()
	if _, running := ci.inFlight[id]; running {
		ci.mu.Unlock()
		return errors.New("a certificate order is already running for this domain")
	}
	p := &pendingOrder{
		proceed: make(chan struct{}),
		cancel:  make(chan struct{}),
		stage:   stageStarting,
	}
	ci.inFlight[id] = p
	ci.mu.Unlock()

	go func() {
		defer func() {
			ci.mu.Lock()
			delete(ci.inFlight, id)
			ci.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), acmeChallengeTimeout+30*time.Minute)
		defer cancel()

		log.Printf("certificate order for %s: mode=%s directory=%s names=%v",
			domain.TLD, domain.CertMode, domain.AcmeDirectory, certificateNames(domain.TLD))

		if err := ci.obtain(ctx, domain, p); err != nil {
			log.Printf("certificate order for %s failed: %v", domain.TLD, err)
			if err := ci.q.SetDomainCertError(ctx, db.SetDomainCertErrorParams{
				ID: domain.ID, CertError: err.Error(),
			}); err != nil {
				log.Printf("could not record the certificate error for %s: %v", domain.TLD, err)
			}
			return
		}
		log.Printf("issued a certificate for %s", domain.TLD)
	}()
	return nil
}

func (ci *certIssuer) obtain(ctx context.Context, domain db.Domain, p *pendingOrder) error {
	if ci.blobs == nil {
		return errNoBlobStore
	}
	if strings.TrimSpace(domain.AcmeEmail) == "" {
		return errors.New("an account email is required to ask a ca for a certificate")
	}

	user, err := ci.acmeUser(ctx, domain)
	if err != nil {
		return err
	}

	cfg := lego.NewConfig(user)
	cfg.CADirURL = directoryURL(domain.AcmeDirectory)
	cfg.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("could not reach the certificate authority: %w", err)
	}

	provider, opts, err := ci.dnsProvider(domain, p)
	if err != nil {
		return err
	}
	if err := client.Challenge.SetDNS01Provider(provider, opts...); err != nil {
		return fmt.Errorf("could not set up the dns challenge: %w", err)
	}

	if user.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("could not register with the certificate authority: %w", err)
		}
		user.Registration = reg
	}

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: certificateNames(domain.TLD),
		Bundle:  true,
	})
	if err != nil {
		return fmt.Errorf("the certificate authority did not issue: %w", err)
	}

	p.setStage(stageStoring)
	return ci.store(ctx, domain, string(res.Certificate), string(res.PrivateKey))
}

func (ci *certIssuer) dnsProvider(domain db.Domain, p *pendingOrder) (challenge.Provider, []dns01.ChallengeOption, error) {
	switch domain.CertMode {
	case certModeACMECloudflare:
		var creds struct {
			APIToken string `json:"api_token"`
		}
		if err := json.Unmarshal([]byte(domain.AcmeCredentials), &creds); err != nil {
			return nil, nil, errors.New("the cloudflare credentials are not readable; save them again")
		}
		if strings.TrimSpace(creds.APIToken) == "" {
			return nil, nil, errors.New("a cloudflare api token is required")
		}
		cf := cloudflare.NewDefaultConfig()
		cf.AuthToken = creds.APIToken
		provider, err := cloudflare.NewDNSProviderConfig(cf)
		if err != nil {
			return nil, nil, fmt.Errorf("could not set up the cloudflare provider: %w", err)
		}
		return provider, nil, nil

	case certModeACMEManual:
		m := &manualProvider{order: p}
		return m, []dns01.ChallengeOption{dns01.WrapPreCheck(m.manualPreCheck)}, nil
	}
	return nil, nil, fmt.Errorf("%q is not a mode that asks a ca for anything", domain.CertMode)
}

func (ci *certIssuer) store(ctx context.Context, domain db.Domain, certPEM, keyPEM string) error {
	info, err := validateCertificate(certPEM, keyPEM, domain.TLD)
	if err != nil {
		return err
	}

	certKey := ci.blobs.newKey("certs", "fullchain.pem")
	keyKey := ci.blobs.newKey("certs", "privkey.pem")
	if err := ci.blobs.Put(ctx, certKey, "application/x-pem-file", strings.NewReader(certPEM)); err != nil {
		return fmt.Errorf("could not store the certificate: %w", err)
	}
	if err := ci.blobs.Put(ctx, keyKey, "application/x-pem-file", strings.NewReader(keyPEM)); err != nil {
		_ = ci.blobs.Delete(ctx, certKey)
		return errors.New("could not store the private key")
	}

	updated, err := ci.q.UpdateDomainCert(ctx, db.UpdateDomainCertParams{
		ID:              domain.ID,
		CertObjectKey:   certKey,
		KeyObjectKey:    keyKey,
		CertFingerprint: info.Fingerprint,
		CertNotAfter:    pgtype.Timestamptz{Time: info.NotAfter, Valid: true},
	})
	if err != nil {
		_ = ci.blobs.Delete(ctx, certKey)
		_ = ci.blobs.Delete(ctx, keyKey)
		return fmt.Errorf("could not record the certificate: %w", err)
	}

	if domain.CertObjectKey != "" && domain.CertObjectKey != certKey {
		_ = ci.blobs.Delete(ctx, domain.CertObjectKey)
		_ = ci.blobs.Delete(ctx, domain.KeyObjectKey)
	}

	ci.PushToDomain(ctx, updated)
	return nil
}

func (ci *certIssuer) PushToClient(ctx context.Context, clientID pgtype.UUID) {
	if !ci.hub.Connected(uuid.UUID(clientID.Bytes).String()) {
		return
	}
	pushDpipeConfig(ctx, ci.q, ci.blobs, ci.hub, clientID)
	pushProxyConfig(ctx, ci.q, ci.hub, ci.proxy, clientID)
	pushIntproxyConfig(ctx, ci.q, ci.blobs, ci.hub, ci.proxy.controlURL, clientID)
}

func (ci *certIssuer) PushToDomain(ctx context.Context, domain db.Domain) {
	ids, err := ci.q.ListClientIDsByDomain(ctx, domain.ID)
	if err != nil {
		log.Printf("could not list the clients of %s: %v", domain.TLD, err)
		return
	}
	sent := 0
	for _, clientID := range ids {
		if !ci.hub.Connected(uuid.UUID(clientID.Bytes).String()) {
			continue
		}
		pushDpipeConfig(ctx, ci.q, ci.blobs, ci.hub, clientID)
		pushProxyConfig(ctx, ci.q, ci.hub, ci.proxy, clientID)
		pushIntproxyConfig(ctx, ci.q, ci.blobs, ci.hub, ci.proxy.controlURL, clientID)
		sent++
	}
	if sent > 0 {
		log.Printf("pushed the tls configuration for %s to %d connected client(s)", domain.TLD, sent)
	}
}

type manualProvider struct {
	order *pendingOrder
}

func (m *manualProvider) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	m.order.addRecord(dnsRecord{Name: info.FQDN, Value: info.Value})
	m.order.setStage(stageWaiting)
	log.Printf("certificate order for %s: needs TXT %s = %q", domain, info.FQDN, info.Value)
	return nil
}

func (m *manualProvider) CleanUp(domain, token, keyAuth string) error {
	m.order.clearRecords()
	return nil
}

func (m *manualProvider) manualPreCheck(domain, fqdn, value string, check dns01.PreCheckFunc) (bool, error) {
	select {
	case <-m.order.proceed:
	case <-m.order.cancel:
		return false, errors.New("the order was cancelled")
	case <-time.After(acmeChallengeTimeout):
		return false, fmt.Errorf("no one confirmed the dns records within %s", acmeChallengeTimeout)
	}

	m.order.setStage(stageValidating)
	ok, err := check(fqdn, value)
	if err != nil {
		return false, fmt.Errorf("%s is not resolving with the expected value yet: %w", fqdn, err)
	}
	return ok, nil
}

type acmeUser struct {
	email        string
	key          crypto.PrivateKey
	Registration *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.Registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

func (ci *certIssuer) acmeUser(ctx context.Context, domain db.Domain) (*acmeUser, error) {
	if domain.AcmeAccountKey != "" {
		key, err := parseECKey(domain.AcmeAccountKey)
		if err != nil {
			return nil, fmt.Errorf("the stored account key for %s is unreadable: %w", domain.TLD, err)
		}
		return &acmeUser{email: domain.AcmeEmail, key: key}, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeECKey(key)
	if err != nil {
		return nil, err
	}
	if err := ci.q.SetDomainAccountKey(ctx, db.SetDomainAccountKeyParams{
		ID: domain.ID, AcmeAccountKey: encoded,
	}); err != nil {
		return nil, fmt.Errorf("could not store the account key: %w", err)
	}
	return &acmeUser{email: domain.AcmeEmail, key: key}, nil
}

func parseECKey(s string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, errors.New("not a PEM block")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func encodeECKey(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}
