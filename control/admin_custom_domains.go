package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type adminCustomDomainDTO struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Status string `json:"status"`

	VMID   string `json:"vm_id"`
	VMName string `json:"vm_name"`

	VMStatus string `json:"vm_status"`
	Client   string `json:"client"`
	Owner    string `json:"owner"`
	Email    string `json:"email"`

	Fingerprint string `json:"cert_fingerprint,omitempty"`
	NotAfter    string `json:"cert_not_after,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func toAdminCustomDomainDTO(row db.ListCustomDomainsRow) adminCustomDomainDTO {
	dto := adminCustomDomainDTO{
		ID:          uuid.UUID(row.ID.Bytes).String(),
		Domain:      row.Domain,
		Status:      row.Status,
		VMID:        uuid.UUID(row.VMID.Bytes).String(),
		VMName:      row.VMName,
		VMStatus:    row.VmStatus,
		Client:      row.ClientHostname,
		Owner:       row.OwnerUsername,
		Email:       row.OwnerEmail,
		Fingerprint: row.CertFingerprint,
		LastError:   row.LastError,
	}
	if row.CertNotAfter.Valid {
		dto.NotAfter = row.CertNotAfter.Time.Format(time.RFC3339)
	}
	if row.CreatedAt.Valid {
		dto.CreatedAt = row.CreatedAt.Time.Format(time.RFC3339)
	}
	return dto
}

func (h *AdminHandler) ListCustomDomains(c *echo.Context) error {
	rows, err := h.q.ListCustomDomains(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list the custom domains")
	}
	items := make([]adminCustomDomainDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminCustomDomainDTO(row))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// DeleteCustomDomain takes a name off the air without touching the certificate
// stored for it, which is what deleting the vm itself does too.
func (h *AdminHandler) DeleteCustomDomain(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid custom domain id")
	}
	ctx := c.Request().Context()

	clientID, err := h.q.GetCustomDomainClient(ctx, pgID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the custom domain")
	}

	if err := h.q.DeleteCustomDomain(ctx, pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not remove the custom domain")
	}
	cancelTasksForSubject(ctx, h.q, subjectCustomDomain, pgID, "an admin removed the custom domain")
	if clientID.Valid {
		pushDpipeConfig(ctx, h.q, h.blobs, h.hub, clientID)
		pushProxyConfig(ctx, h.q, h.hub, h.proxy, clientID)
	}
	return c.NoContent(http.StatusNoContent)
}

type adminCustomCertDTO struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`

	Owner string `json:"owner"`
	Email string `json:"email"`

	VMName string `json:"vm_name,omitempty"`
	Status string `json:"domain_status,omitempty"`

	Fingerprint string `json:"cert_fingerprint,omitempty"`
	NotAfter    string `json:"cert_not_after"`
	IssuedAt    string `json:"cert_issued_at,omitempty"`
	ExpiresIn   int    `json:"expires_in_days"`
	Expired     bool   `json:"expired"`
	Attached    bool   `json:"attached"`
}

func toAdminCustomCertDTO(row db.ListCustomDomainCertsRow) adminCustomCertDTO {
	dto := adminCustomCertDTO{
		ID:          uuid.UUID(row.ID.Bytes).String(),
		Domain:      row.Domain,
		Owner:       row.OwnerUsername,
		Email:       row.OwnerEmail,
		VMName:      row.VMName,
		Status:      row.DomainStatus,
		Fingerprint: row.CertFingerprint,
		Attached:    row.DomainStatus != "",
	}
	if row.CertNotAfter.Valid {
		dto.NotAfter = row.CertNotAfter.Time.Format(time.RFC3339)
		dto.ExpiresIn = int(time.Until(row.CertNotAfter.Time).Hours() / 24)
		dto.Expired = !row.CertNotAfter.Time.After(time.Now())
	}
	if row.CertIssuedAt.Valid {
		dto.IssuedAt = row.CertIssuedAt.Time.Format(time.RFC3339)
	}
	return dto
}

func (h *AdminHandler) ListCustomDomainCerts(c *echo.Context) error {
	rows, err := h.q.ListCustomDomainCerts(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list the stored certificates")
	}
	items := make([]adminCustomCertDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminCustomCertDTO(row))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// DeleteCustomDomainCert throws the stored certificate away for good. A name a
// vm is still bound to is refused: taking the certificate out from under a host
// that is serving with it is a separate decision, so remove the binding first.
func (h *AdminHandler) DeleteCustomDomainCert(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid certificate id")
	}
	ctx := c.Request().Context()

	row, err := h.q.GetCustomDomainCertByID(ctx, pgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such certificate")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the certificate")
	}
	if _, err := h.q.GetCustomDomainByDomain(ctx, row.Domain); err == nil {
		return echo.NewHTTPError(http.StatusConflict,
			"a vm is still bound to "+row.Domain+"; remove the custom domain first")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the custom domain")
	}

	if err := h.q.DeleteCustomDomainCert(ctx, row.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not remove the certificate")
	}
	if h.blobs != nil {
		_ = h.blobs.Delete(ctx, row.CertObjectKey)
		_ = h.blobs.Delete(ctx, row.KeyObjectKey)
	}
	return c.NoContent(http.StatusNoContent)
}
