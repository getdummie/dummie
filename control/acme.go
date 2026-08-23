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

// Issuance runs here rather than on the qemu hosts, and the reason is the
// challenge type. A fleet needs "*.<domain>" and "*.console.<domain>" on one
// certificate, two wildcards can only be proved over dns-01, and dns-01 involves
// no inbound http at all -- so nothing about it wants to happen on the machine
// that serves the traffic.
//
// Doing it here also keeps the dns credential in one place instead of on every
// host, gives the ca one account and one rate-limit budget per domain instead of
// one per machine, and removes the race that several hosts writing the same
// _acme-challenge TXT record would otherwise be.

// certModes are the ways a domain's certificate can be obtained. Validated in Go
// rather than by a CHECK constraint, so adding one is not a migration.
const (
	// certModeUpload is an operator-supplied certificate. The only option for a
	// tld no public ca will sign -- an internal suffix like "lab.internal" -- and
	// the way to use a corporate ca.
	certModeUpload = "upload"
	// certModeACMEManual is dns-01 with a person in the middle: the server asks
	// for the order, shows the TXT records, and waits for someone to say they are
	// in place. Real certificates without handing over a zone credential, at the
	// cost of not being able to renew unattended.
	certModeACMEManual = "acme_manual"
	// certModeACMECloudflare is dns-01 driven by the api. The only mode that
	// renews without anyone present.
	certModeACMECloudflare = "acme_cloudflare"
)

const (
	acmeDirectoryStaging    = "staging"
	acmeDirectoryProduction = "production"
)

// acmeChallengeTimeout bounds how long an order waits for a person to create the
// TXT records. Generous because the wait is someone opening a registrar's
// control panel, and short enough that a forgotten browser tab does not leave an
// order open all week.
const acmeChallengeTimeout = 30 * time.Minute

// certRenewBefore is how close to expiry a certificate is replaced. Let's
// Encrypt issues for 90 days and recommends renewing at 30 remaining, which
// leaves room for a fortnight of failures before anything is visible.
const certRenewBefore = 30 * 24 * time.Hour

func validCertMode(s string) bool {
	switch s {
	case certModeUpload, certModeACMEManual, certModeACMECloudflare:
		return true
	}
	return false
}

// directoryURL maps the stored name onto a ca. Anything unrecognised is staging:
// the failure mode of guessing wrong towards production is a spent rate limit,
// and towards staging is a certificate no browser trusts -- which is noticed
// immediately and costs nothing.
func directoryURL(s string) string {
	if s == acmeDirectoryProduction {
		return lego.LEDirectoryProduction
	}
	return lego.LEDirectoryStaging
}

// --- pending challenges ----------------------------------------------------

// dnsRecord is one TXT record an operator has to create for a manual order.
type dnsRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// pendingOrder is a manual order that has published its challenges and is
// waiting to be told they are live.
//
// It is in memory and nowhere else. An order is a live conversation with the ca
// -- lego holds the http client, the nonces and the authorization urls -- and
// none of that survives a restart, so writing the records to a table would only
// make a dead order look resumable. A control server that restarts mid-flow
// drops the order, and the admin starts another.
type pendingOrder struct {
	// mu guards records only. It is the order's own rather than the issuer's
	// because the writer is lego's goroutine, deep inside Obtain, and the reader
	// is whatever request is polling the screen.
	mu      sync.Mutex
	records []dnsRecord

	// proceed is closed when the operator confirms. Every Present call blocks on
	// it, so an order with several authorizations waits once, not once per name.
	proceed chan struct{}
	cancel  chan struct{}

	// Both closes go through a Once. An admin double-clicking either button would
	// otherwise close a closed channel, which is a panic that takes the server
	// down rather than an error the screen can show.
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

// snapshot returns a copy, so a caller ranging over it cannot be reading the
// slice lego is appending to.
func (p *pendingOrder) snapshot() []dnsRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]dnsRecord(nil), p.records...)
}

// certIssuer owns every in-flight issuance. One per process, constructed
// alongside the task runner.
//
// Issuance cannot be a scheduled task: the runner's timeout is thirty seconds,
// an automated dns-01 order takes minutes waiting for propagation, and a manual
// one takes as long as a person does. Tasks start work here and return; the
// result lands on the domain row, which is what the admin screen reads.
type certIssuer struct {
	q     *db.Queries
	blobs *blobStore
	hub   *Hub
	proxy proxyAuthConfig

	mu sync.Mutex
	// inFlight is what stops a domain having two orders at once -- which would be
	// two sets of TXT records on one name, and two accounts' worth of rate limit.
	inFlight map[string]*pendingOrder
}

func newCertIssuer(q *db.Queries, blobs *blobStore, hub *Hub, proxy proxyAuthConfig) *certIssuer {
	return &certIssuer{q: q, blobs: blobs, hub: hub, proxy: proxy, inFlight: map[string]*pendingOrder{}}
}

// Pending returns the TXT records a manual order is waiting on, if there is one.
func (ci *certIssuer) Pending(domainID pgtype.UUID) ([]dnsRecord, bool) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	p, ok := ci.inFlight[uuid.UUID(domainID.Bytes).String()]
	if !ok {
		return nil, false
	}
	return p.snapshot(), true
}

// Confirm tells a waiting manual order that its records are live.
func (ci *certIssuer) Confirm(domainID pgtype.UUID) error {
	ci.mu.Lock()
	p, ok := ci.inFlight[uuid.UUID(domainID.Bytes).String()]
	ci.mu.Unlock()
	if !ok {
		return errors.New("there is no certificate order waiting for this domain")
	}
	if len(p.snapshot()) == 0 {
		return errors.New("the order has not published its challenge yet")
	}
	p.confirm()
	return nil
}

// Cancel abandons an in-flight order.
//
// It releases a guided order that is blocked waiting for a person, which is the
// case worth cancelling -- one that would otherwise sit there for half an hour.
// An automated order is not interrupted: it is already talking to the ca and to
// cloudflare, and there is no safe point to stop it at. It finishes on its own
// and its entry clears then.
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

// Start begins an issuance in the background and returns immediately. The
// caller's context is deliberately not carried into it: the order outlives the
// request that asked for it, and a browser navigating away must not abandon a
// conversation the ca has already started.
func (ci *certIssuer) Start(domain db.Domain) error {
	id := uuid.UUID(domain.ID.Bytes).String()

	ci.mu.Lock()
	if _, running := ci.inFlight[id]; running {
		ci.mu.Unlock()
		return errors.New("a certificate order is already running for this domain")
	}
	p := &pendingOrder{proceed: make(chan struct{}), cancel: make(chan struct{})}
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

// obtain runs one order end to end and stores the result.
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
	// EC256 rather than RSA: smaller handshakes, and every client that reaches
	// these hosts is a current browser or ssh client.
	cfg.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("could not reach the certificate authority: %w", err)
	}

	provider, err := ci.dnsProvider(domain, p)
	if err != nil {
		return err
	}
	// The dns-01 solver's own propagation check is left on: it is the thing that
	// stops an order being submitted to the ca before the record is actually
	// visible, which is the difference between waiting a minute and spending an
	// authorization failure.
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
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
		Bundle:  true, // the chain, not just the leaf -- a missing intermediate fails in browsers and passes in curl
	})
	if err != nil {
		return fmt.Errorf("the certificate authority did not issue: %w", err)
	}

	return ci.store(ctx, domain, string(res.Certificate), string(res.PrivateKey))
}

// dnsProvider builds the solver for a domain's mode.
func (ci *certIssuer) dnsProvider(domain db.Domain, p *pendingOrder) (challenge.Provider, error) {
	switch domain.CertMode {
	case certModeACMECloudflare:
		var creds struct {
			APIToken string `json:"api_token"`
		}
		if err := json.Unmarshal([]byte(domain.AcmeCredentials), &creds); err != nil {
			return nil, errors.New("the cloudflare credentials are not readable; save them again")
		}
		if strings.TrimSpace(creds.APIToken) == "" {
			return nil, errors.New("a cloudflare api token is required")
		}
		cf := cloudflare.NewDefaultConfig()
		cf.AuthToken = creds.APIToken
		provider, err := cloudflare.NewDNSProviderConfig(cf)
		if err != nil {
			return nil, fmt.Errorf("could not set up the cloudflare provider: %w", err)
		}
		return provider, nil

	case certModeACMEManual:
		return &manualProvider{order: p}, nil
	}
	return nil, fmt.Errorf("%q is not a mode that asks a ca for anything", domain.CertMode)
}

// store validates what came back, puts it in the bucket, records it, and pushes
// it to every host on the domain.
//
// Validated even though this server just obtained it: the check is what
// guarantees both wildcards are on the certificate, and a ca that quietly
// dropped a name would otherwise produce a fleet whose consoles fail and whose
// guests work.
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
		// The certificate half is already up and nothing points at it; remove it
		// rather than leave an object no row will ever name.
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
		// New objects, old row: the fleet keeps serving what it has. The two orphans
		// are the price of not pointing the row at something that might not be there.
		_ = ci.blobs.Delete(ctx, certKey)
		_ = ci.blobs.Delete(ctx, keyKey)
		return fmt.Errorf("could not record the certificate: %w", err)
	}

	// After the row, so a host that connects during the push reads the same
	// certificate the push is carrying.
	if domain.CertObjectKey != "" && domain.CertObjectKey != certKey {
		_ = ci.blobs.Delete(ctx, domain.CertObjectKey)
		_ = ci.blobs.Delete(ctx, domain.KeyObjectKey)
	}

	ci.PushToDomain(ctx, updated)
	return nil
}

// PushToClient sends one host both files that depend on which domain it belongs
// to. Used when that changes, which is a different event from the certificate
// changing but has the same two consequences.
func (ci *certIssuer) PushToClient(ctx context.Context, clientID pgtype.UUID) {
	if !ci.hub.Connected(uuid.UUID(clientID.Bytes).String()) {
		return
	}
	pushDpipeConfig(ctx, ci.q, ci.blobs, ci.hub, clientID)
	pushProxyConfig(ctx, ci.q, ci.hub, ci.proxy, clientID)
}

// PushToDomain sends every connected host on a domain both files that depend on
// its certificate.
//
// Both, not just dpipe's: turning tls on also opens proxy's 443 ingress and
// makes the session cookie Secure, and a host that got one without the other is
// either terminating tls nobody can reach or serving https with a cookie the
// browser drops.
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
		sent++
	}
	if sent > 0 {
		log.Printf("pushed the tls configuration for %s to %d connected client(s)", domain.TLD, sent)
	}
}

// --- the manual solver -----------------------------------------------------

// manualProvider is the dns-01 solver for the guided mode: it publishes nothing
// itself, it records what has to be published and waits for a person.
type manualProvider struct {
	order *pendingOrder
}

// Present records the TXT record for one authorization and blocks until the
// operator confirms.
//
// lego calls this once per name in the order, sequentially, so the records
// accumulate and only the last call actually waits -- the earlier ones return as
// soon as the operator confirms, which they do together. The alternative,
// waiting per record, would ask someone to click three times for one order.
func (m *manualProvider) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	m.order.addRecord(dnsRecord{Name: info.FQDN, Value: info.Value})

	select {
	case <-m.order.proceed:
		return nil
	case <-m.order.cancel:
		return errors.New("the order was cancelled")
	case <-time.After(acmeChallengeTimeout):
		return fmt.Errorf("no one confirmed the dns records within %s", acmeChallengeTimeout)
	}
}

// CleanUp forgets the record. There is nothing to remove at a registrar from
// here -- the operator created it and the operator removes it -- so this only
// stops the admin screen still showing it.
func (m *manualProvider) CleanUp(domain, token, keyAuth string) error {
	m.order.clearRecords()
	return nil
}

// --- the acme account ------------------------------------------------------

// acmeUser is lego's view of the account. The key is the domain's own, stored on
// its row and generated once: a new key on every issuance would register a new
// account with the ca each time, which is its own rate limit.
type acmeUser struct {
	email        string
	key          crypto.PrivateKey
	Registration *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.Registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// acmeUser loads the domain's account key, generating and storing one the first
// time it is asked for.
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
	// Stored before the account is registered. The other order would risk
	// registering with a key this server then forgot, which leaves an account at
	// the ca that nothing can ever use again.
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
