package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// The certificate screen is per domain rather than a fleet setting, because a
// dns credential is a fact about one zone. An installation with two domains at
// two registrars has no single provider to name.

// certificateDTO is the whole certificate state of one domain.
//
// AcmeCredentials is absent by construction, not blanked on the way out: the
// struct has no field for it. A token that can rewrite an operator's zone should
// not be one refactor away from being serialised, so the only signal here is
// whether one is stored.
type certificateDTO struct {
	DomainID      string `json:"domain_id"`
	TLD           string `json:"tld"`
	TLSEnabled    bool   `json:"tls_enabled"`
	CertMode      string `json:"cert_mode"`
	ACMEDirectory string `json:"acme_directory"`
	ACMEEmail     string `json:"acme_email"`
	CredentialSet bool   `json:"credential_set"`

	// Names is what a certificate for this domain has to cover, so the screen can
	// show an operator what to expect before anything is issued -- and so the
	// second wildcard, which is the one people leave off, is visible.
	Names []string `json:"names"`

	Fingerprint string `json:"fingerprint"`
	NotAfter    string `json:"not_after,omitempty"`
	IssuedAt    string `json:"issued_at,omitempty"`
	ExpiresIn   int    `json:"expires_in_days,omitempty"`
	Error       string `json:"error,omitempty"`

	// Pending is set while a guided order is waiting for its TXT records to be
	// created. InFlight without Pending means an order is running but has not
	// published a challenge yet.
	//
	// Stage says which of those it is, and what a slow order is currently blocked
	// on -- the difference between "nothing has happened yet", "your records are
	// not resolving" and "the ca is deciding", which otherwise all present as a
	// screen that is not changing.
	InFlight bool        `json:"in_flight"`
	Stage    string      `json:"stage,omitempty"`
	Pending  []dnsRecord `json:"pending,omitempty"`
}

func (h *AdminHandler) certificateDTO(d db.Domain) certificateDTO {
	dto := certificateDTO{
		DomainID:      uuid.UUID(d.ID.Bytes).String(),
		TLD:           d.TLD,
		TLSEnabled:    d.TlsEnabled,
		CertMode:      d.CertMode,
		ACMEDirectory: d.AcmeDirectory,
		ACMEEmail:     d.AcmeEmail,
		CredentialSet: d.AcmeCredentials != "",
		Names:         certificateNames(d.TLD),
		Fingerprint:   d.CertFingerprint,
		Error:         d.CertError,
	}
	if d.CertNotAfter.Valid {
		dto.NotAfter = d.CertNotAfter.Time.Format(time.RFC3339)
		// Rounded down, so "0 days" means today rather than "some time in the next
		// twenty-four hours".
		dto.ExpiresIn = int(time.Until(d.CertNotAfter.Time).Hours() / 24)
	}
	if d.CertIssuedAt.Valid {
		dto.IssuedAt = d.CertIssuedAt.Time.Format(time.RFC3339)
	}
	if h.certs != nil {
		records, stage, ok := h.certs.Pending(d.ID)
		dto.InFlight = ok
		dto.Stage = stage
		dto.Pending = records
	}
	return dto
}

// GetCertificate reports one domain's certificate state.
func (h *AdminHandler) GetCertificate(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, h.certificateDTO(domain))
}

// updateTLSReq is the operator-editable half of the certificate settings.
//
// ACMECredentials is write-only and optional: an empty value leaves whatever is
// stored in place, so saving the form without retyping a token does not wipe it.
// The consequence is that the only way to remove one is to change mode, which is
// the same bargain the secret settings make.
type updateTLSReq struct {
	Enabled         bool   `json:"enabled"`
	CertMode        string `json:"cert_mode"`
	ACMEDirectory   string `json:"acme_directory"`
	ACMEEmail       string `json:"acme_email"`
	ACMECredentials string `json:"acme_credentials"`
}

// UpdateTLS writes the settings and, when tls is turned on or off, tells the
// hosts on the domain.
//
// Turning it on with no certificate stored changes nothing on any host: the
// generated dpipe config only enables tls when there is something to serve, and
// a host told otherwise would refuse to start. The flag is the operator's intent,
// and it takes effect the moment a certificate exists.
func (h *AdminHandler) UpdateTLS(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}

	var req updateTLSReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if !validCertMode(req.CertMode) {
		return echo.NewHTTPError(http.StatusBadRequest, "unknown certificate mode")
	}
	directory := strings.TrimSpace(req.ACMEDirectory)
	if directory != acmeDirectoryStaging && directory != acmeDirectoryProduction {
		return echo.NewHTTPError(http.StatusBadRequest, "the directory must be staging or production")
	}
	email := strings.TrimSpace(req.ACMEEmail)
	if req.CertMode != certModeUpload && email == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "an account email is required to ask a ca for a certificate")
	}
	// Refused rather than served with no certificate: an operator turning this on
	// is asking for https, and silently doing nothing is a worse answer than saying
	// what is missing.
	if req.Enabled && domain.CertObjectKey == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "this domain has no certificate yet, so tls cannot be turned on")
	}

	updated, err := h.q.UpdateDomainTLS(c.Request().Context(), db.UpdateDomainTLSParams{
		ID:              domain.ID,
		TlsEnabled:      req.Enabled,
		CertMode:        req.CertMode,
		AcmeDirectory:   directory,
		AcmeEmail:       email,
		AcmeCredentials: strings.TrimSpace(req.ACMECredentials),
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the certificate settings")
	}

	if updated.TlsEnabled != domain.TlsEnabled && h.certs != nil {
		h.certs.PushToDomain(c.Request().Context(), updated)
	}
	return c.JSON(http.StatusOK, h.certificateDTO(updated))
}

// uploadCertReq is an operator-supplied certificate.
type uploadCertReq struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// UploadCertificate stores a certificate the operator obtained themselves. The
// only route to https for a tld no public ca will sign, and the way to use a
// corporate one.
func (h *AdminHandler) UploadCertificate(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}
	if h.blobs == nil || h.certs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}

	var req uploadCertReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	certPEM, err := normalizePEM(req.CertPEM)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "the certificate is not a PEM block")
	}
	keyPEM, err := normalizePEM(req.KeyPEM)
	if err != nil {
		// Not "the key is not a PEM block" plus the input: nothing on this path
		// echoes any part of a private key back.
		return echo.NewHTTPError(http.StatusBadRequest, "the private key is not a PEM block")
	}

	// Validated here as well as in store, so a bad paste is a 400 with the reason
	// rather than a background failure the operator has to go looking for.
	if _, err := validateCertificate(certPEM, keyPEM, domain.TLD); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := h.certs.store(c.Request().Context(), domain, certPEM, keyPEM); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	updated, err := h.q.GetDomain(c.Request().Context(), domain.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "the certificate was stored but could not be read back")
	}
	return c.JSON(http.StatusOK, h.certificateDTO(updated))
}

// IssueCertificate starts an acme order. It returns as soon as the order is
// running: an automated one takes minutes waiting for dns to propagate, and a
// guided one takes as long as a person does, so neither can be a request.
func (h *AdminHandler) IssueCertificate(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}
	if h.certs == nil || h.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}
	if domain.CertMode == certModeUpload {
		return echo.NewHTTPError(http.StatusBadRequest, "this domain is set to use an uploaded certificate")
	}
	if err := h.certs.Start(domain); err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	return c.JSON(http.StatusAccepted, h.certificateDTO(domain))
}

// ContinueCertificate tells a waiting guided order that its TXT records exist.
func (h *AdminHandler) ContinueCertificate(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}
	if h.certs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}
	if err := h.certs.Confirm(domain.ID); err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// CancelCertificate abandons an in-flight order.
func (h *AdminHandler) CancelCertificate(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}
	if h.certs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}
	if err := h.certs.Cancel(domain.ID); err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// DeleteCertificate forgets a domain's certificate and takes its hosts back to
// plain http.
//
// The objects are removed after the row, and the hosts are told after that. Any
// other order leaves a window where a host is asked for a certificate this
// server has already deleted.
func (h *AdminHandler) DeleteCertificate(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	updated, err := h.q.ClearDomainCert(ctx, domain.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not remove the certificate")
	}
	if h.blobs != nil && domain.CertObjectKey != "" {
		_ = h.blobs.Delete(ctx, domain.CertObjectKey)
		_ = h.blobs.Delete(ctx, domain.KeyObjectKey)
	}
	if h.certs != nil {
		h.certs.PushToDomain(ctx, updated)
	}
	return c.JSON(http.StatusOK, h.certificateDTO(updated))
}

// domainParam resolves the :id in the path to a row.
func (h *AdminHandler) domainParam(c *echo.Context) (db.Domain, error) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return db.Domain{}, echo.NewHTTPError(http.StatusBadRequest, "invalid domain id")
	}
	domain, err := h.q.GetDomain(c.Request().Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Domain{}, echo.NewHTTPError(http.StatusNotFound, "domain not found")
	}
	if err != nil {
		return db.Domain{}, echo.NewHTTPError(http.StatusInternalServerError, "could not load the domain")
	}
	return domain, nil
}
