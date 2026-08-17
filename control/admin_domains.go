package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type domainDTO struct {
	ID  string `json:"id"`
	TLD string `json:"tld"`
}

func toDomainDTO(d db.Domain) domainDTO {
	return domainDTO{ID: uuid.UUID(d.ID.Bytes).String(), TLD: d.TLD}
}

// normalizeTLD validates and canonicalises an operator-entered domain. It checks
// the shape of a hostname (hostnamePattern, shared with the proxy generator that
// has to emit these) and deliberately not the public suffix list: these are the
// operator's own suffixes, and are often internal ones like "lab.internal" that
// no registry knows about.
func normalizeTLD(s string) (string, error) {
	// A leading dot is how people write a suffix, and a trailing one is a
	// fully-qualified name; neither should become a second stored form of the
	// same domain.
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

// ListDomains returns every domain, unpaginated: an installation has a handful
// of these, and the enrollment behaviour depends on the total, so a page of them
// would be a misleading thing to show.
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

// DeleteDomain removes a domain. Clients pointing at it are left in place with no
// domain (ON DELETE SET NULL) rather than blocking the delete.
func (h *AdminHandler) DeleteDomain(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid domain id")
	}
	if err := h.q.DeleteDomain(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete domain")
	}
	return c.NoContent(http.StatusNoContent)
}
