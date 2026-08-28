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

type certificateDTO struct {
	DomainID      string `json:"domain_id"`
	TLD           string `json:"tld"`
	TLSEnabled    bool   `json:"tls_enabled"`
	CertMode      string `json:"cert_mode"`
	ACMEDirectory string `json:"acme_directory"`
	ACMEEmail     string `json:"acme_email"`
	CredentialSet bool   `json:"credential_set"`

	Names []string `json:"names"`

	Fingerprint string `json:"fingerprint"`
	NotAfter    string `json:"not_after,omitempty"`
	IssuedAt    string `json:"issued_at,omitempty"`
	ExpiresIn   int    `json:"expires_in_days,omitempty"`
	Error       string `json:"error,omitempty"`

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

func (h *AdminHandler) GetCertificate(c *echo.Context) error {
	domain, err := h.domainParam(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, h.certificateDTO(domain))
}

type updateTLSReq struct {
	Enabled         bool   `json:"enabled"`
	CertMode        string `json:"cert_mode"`
	ACMEDirectory   string `json:"acme_directory"`
	ACMEEmail       string `json:"acme_email"`
	ACMECredentials string `json:"acme_credentials"`
}

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

type uploadCertReq struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

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
		return echo.NewHTTPError(http.StatusBadRequest, "the private key is not a PEM block")
	}

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
