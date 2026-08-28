package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type domainDTO struct {
	ID  string `json:"id"`
	TLD string `json:"tld"`
	TLSEnabled bool   `json:"tls_enabled"`
	HasCert    bool   `json:"has_cert"`
	NotAfter   string `json:"cert_not_after,omitempty"`
	CertError  string `json:"cert_error,omitempty"`
}

func toDomainDTO(d db.Domain) domainDTO {
	dto := domainDTO{
		ID:         uuid.UUID(d.ID.Bytes).String(),
		TLD:        d.TLD,
		TLSEnabled: d.TlsEnabled,
		HasCert:    d.CertObjectKey != "",
		CertError:  d.CertError,
	}
	if d.CertNotAfter.Valid {
		dto.NotAfter = d.CertNotAfter.Time.Format(time.RFC3339)
	}
	return dto
}

func normalizeTLD(s string) (string, error) {
	tld := strings.Trim(strings.ToLower(strings.TrimSpace(s)), ".")
	if tld == "" {
		return "", errors.New("tld is required")
	}
	if len(tld) > 253 {
		return "", errors.New("tld is too long")
	}
	if !hostnamePattern.MatchString(tld) {
		return "", errors.New("tld must be a dotted name of letters, digits and hyphens")
	}
	return tld, nil
}

func (h *AdminHandler) ListDomains(c *echo.Context) error {
	rows, err := h.q.ListDomains(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list domains")
	}
	items := make([]domainDTO, 0, len(rows))
	for _, d := range rows {
		items = append(items, toDomainDTO(d))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type createDomainReq struct {
	TLD string `json:"tld"`
}

func (h *AdminHandler) CreateDomain(c *echo.Context) error {
	var req createDomainReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	tld, err := normalizeTLD(req.TLD)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	d, err := h.q.CreateDomain(c.Request().Context(), tld)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "that domain already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create domain")
	}
	return c.JSON(http.StatusCreated, toDomainDTO(d))
}

func (h *AdminHandler) DeleteDomain(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid domain id")
	}
	ctx := c.Request().Context()

	domain, certErr := h.q.GetDomain(ctx, pgID)

	if err := h.q.DeleteDomain(ctx, pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete domain")
	}
	if certErr == nil && h.blobs != nil && domain.CertObjectKey != "" {
		_ = h.blobs.Delete(ctx, domain.CertObjectKey)
		_ = h.blobs.Delete(ctx, domain.KeyObjectKey)
	}
	return c.NoContent(http.StatusNoContent)
}
