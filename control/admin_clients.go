package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// --- enrollment keys --------------------------------------------------------

type clientKeyDTO struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	KeyPrefix string `json:"key_prefix"`
	Uses      int32  `json:"uses"`
	MaxUses   *int32 `json:"max_uses"`   // null = unlimited
	ExpiresAt string `json:"expires_at"` // "" = never
	Status    string `json:"status"`     // active | revoked | expired | exhausted
	CreatedAt string `json:"created_at"`
}

func toClientKeyDTO(k db.ClientEnrollmentKey) clientKeyDTO {
	d := clientKeyDTO{
		ID:        uuid.UUID(k.ID.Bytes).String(),
		Label:     k.Label,
		KeyPrefix: k.KeyPrefix,
		Uses:      k.Uses,
		CreatedAt: k.CreatedAt.Time.Format(time.RFC3339),
	}
	if k.MaxUses.Valid {
		v := k.MaxUses.Int32
		d.MaxUses = &v
	}
	if k.ExpiresAt.Valid {
		d.ExpiresAt = k.ExpiresAt.Time.Format(time.RFC3339)
	}

	// Same precedence the SQL uses when deciding whether a key still works.
	switch {
	case k.Revoked:
		d.Status = "revoked"
	case k.ExpiresAt.Valid && k.ExpiresAt.Time.Before(time.Now()):
		d.Status = "expired"
	case k.MaxUses.Valid && k.Uses >= k.MaxUses.Int32:
		d.Status = "exhausted"
	default:
		d.Status = "active"
	}
	return d
}

func (h *AdminHandler) ListClientKeys(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	total, err := h.q.CountEnrollmentKeys(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count enrollment keys")
	}
	rows, err := h.q.ListEnrollmentKeys(ctx, db.ListEnrollmentKeysParams{Limit: limit, Offset: offset})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list enrollment keys")
	}
	items := make([]clientKeyDTO, 0, len(rows))
	for _, k := range rows {
		items = append(items, toClientKeyDTO(k))
	}
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

type createClientKeyReq struct {
	Label          string `json:"label"`
	MaxUses        *int32 `json:"max_uses"`         // null/omitted = unlimited
	ExpiresInHours *int32 `json:"expires_in_hours"` // null/omitted = never
}

func (h *AdminHandler) CreateClientKey(c *echo.Context) error {
	var req createClientKeyReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.MaxUses != nil && *req.MaxUses < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "max_uses must be at least 1")
	}
	if req.ExpiresInHours != nil && *req.ExpiresInHours < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "expires_in_hours must be at least 1")
	}

	raw, err := newRefreshToken()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not generate key")
	}

	params := db.CreateEnrollmentKeyParams{
		KeyHash:   hashRefresh(raw),
		KeyPrefix: raw[:8],
		Label:     strings.TrimSpace(req.Label),
	}
	if req.MaxUses != nil {
		params.MaxUses = pgtype.Int4{Int32: *req.MaxUses, Valid: true}
	}
	if req.ExpiresInHours != nil {
		exp := time.Now().Add(time.Duration(*req.ExpiresInHours) * time.Hour)
		params.ExpiresAt = pgtype.Timestamptz{Time: exp, Valid: true}
	}
	if uid, _ := c.Get("uid").(string); uid != "" {
		if pgID, err := parseUUID(uid); err == nil {
			params.CreatedBy = pgID
		}
	}

	k, err := h.q.CreateEnrollmentKey(c.Request().Context(), params)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create enrollment key")
	}

	// The raw key is returned exactly once; only its hash is stored.
	return c.JSON(http.StatusCreated, map[string]any{
		"key":  raw,
		"item": toClientKeyDTO(k),
	})
}

func (h *AdminHandler) RevokeClientKey(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid key id")
	}
	if err := h.q.RevokeEnrollmentKey(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not revoke key")
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) DeleteClientKey(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid key id")
	}
	if err := h.q.DeleteEnrollmentKey(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete key")
	}
	return c.NoContent(http.StatusNoContent)
}

// --- clients -----------------------------------------------------------------

type clientDTO struct {
	ID            string `json:"id"`
	MachineID     string `json:"machine_id"`
	Hostname      string `json:"hostname"`
	Status        string `json:"status"` // online | offline | revoked
	Connected     bool   `json:"connected"`
	OS            string `json:"os"`
	OSVersion     string `json:"os_version"`
	Arch          string `json:"arch"`
	ClientVersion string `json:"client_version"`
	LastSeenAt    string `json:"last_seen_at"`
	LastIP        string `json:"last_ip"`
	CreatedAt     string `json:"created_at"`
	// "" when the client has no domain: none was configured when it enrolled, or
	// there were several and the choice was left to an operator.
	Domain string `json:"domain"`
	// DomainID is what the assignment control writes back, and is "" alongside
	// Domain. Both, because the screen shows one and edits the other.
	DomainID string `json:"domain_id"`

	Metrics clientMetricsDTO `json:"metrics"`
}

// clientMetricsDTO is the host snapshot the client last reported. ReportedAt is
// "" when it never has -- the numbers below it are zero either way, so this is
// the only thing that distinguishes an idle host from a silent one.
type clientMetricsDTO struct {
	ReportedAt     string  `json:"reported_at"`
	CPUCount       int32   `json:"cpu_count"`
	CPUPercent     float64 `json:"cpu_percent"`
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
	MemTotalBytes  int64   `json:"mem_total_bytes"`
	MemUsedBytes   int64   `json:"mem_used_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
	DiskUsedBytes  int64   `json:"disk_used_bytes"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
}

// domainIDString renders a nullable domain reference as the empty string rather
// than as the zero uuid, which is what the assignment control sends back to mean
// "no domain".
func domainIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func toClientDTO(a db.Client, connected bool, domain string) clientDTO {
	d := clientDTO{
		ID:            uuid.UUID(a.ID.Bytes).String(),
		MachineID:     a.MachineID,
		Hostname:      a.Hostname,
		Status:        a.Status,
		Connected:     connected,
		Domain:        domain,
		DomainID:      domainIDString(a.DomainID),
		OS:            a.OS,
		OSVersion:     a.OSVersion,
		Arch:          a.Arch,
		ClientVersion: a.ClientVersion,
		LastIP:        a.LastIP,
		CreatedAt:     a.CreatedAt.Time.Format(time.RFC3339),
		Metrics: clientMetricsDTO{
			CPUCount:       a.CPUCount,
			CPUPercent:     a.CPUPercent,
			Load1:          a.Load1,
			Load5:          a.Load5,
			Load15:         a.Load15,
			MemTotalBytes:  a.MemTotalBytes,
			MemUsedBytes:   a.MemUsedBytes,
			DiskTotalBytes: a.DiskTotalBytes,
			DiskUsedBytes:  a.DiskUsedBytes,
			UptimeSeconds:  a.UptimeSeconds,
		},
	}
	if a.MetricsAt.Valid {
		d.Metrics.ReportedAt = a.MetricsAt.Time.Format(time.RFC3339)
	}
	if a.LastSeenAt.Valid {
		d.LastSeenAt = a.LastSeenAt.Time.Format(time.RFC3339)
	}
	if a.Revoked {
		d.Status = "revoked"
	}
	return d
}

func (h *AdminHandler) ListClients(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	total, err := h.q.CountClients(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count clients")
	}
	rows, err := h.q.ListClients(ctx, db.ListClientsParams{Limit: limit, Offset: offset})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list clients")
	}
	items := make([]clientDTO, 0, len(rows))
	for _, r := range rows {
		id := uuid.UUID(r.Client.ID.Bytes).String()
		items = append(items, toClientDTO(r.Client, h.hub.Connected(id), r.DomainTLD.String))
	}
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func (h *AdminHandler) GetClient(c *echo.Context) error {
	id := c.Param("id")
	pgID, err := parseUUID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	ctx := c.Request().Context()
	a, err := h.q.GetClientByID(ctx, pgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such client")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read client")
	}

	// The list endpoint joins the domain in; one row does not have a query that
	// does, and an installation holds a handful of domains, so it is cheaper to
	// scan them than to add a join for a single lookup. Best-effort: a failure
	// here costs the TLD, not the client.
	domain := ""
	if a.DomainID.Valid {
		if rows, err := h.q.ListDomains(ctx); err == nil {
			for _, d := range rows {
				if d.ID == a.DomainID {
					domain = d.TLD
					break
				}
			}
		}
	}
	return c.JSON(http.StatusOK, toClientDTO(a, h.hub.Connected(id), domain))
}

// RevokeClient invalidates the client's token and kicks its live socket, so the
// connection cannot outlive its authorization.
func (h *AdminHandler) RevokeClient(c *echo.Context) error {
	id := c.Param("id")
	pgID, err := parseUUID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	if err := h.q.RevokeClient(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not revoke client")
	}
	h.hub.Kick(id, "client revoked")
	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) DeleteClient(c *echo.Context) error {
	id := c.Param("id")
	pgID, err := parseUUID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	if err := h.q.DeleteClient(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete client")
	}
	h.hub.Kick(id, "client deleted")
	return c.NoContent(http.StatusNoContent)
}

// setClientDomainReq names the domain a host belongs to. An empty string clears
// it.
type setClientDomainReq struct {
	DomainID string `json:"domain_id"`
}

// SetClientDomain moves one host to a domain.
//
// Enrollment picks a domain only when there is exactly one to pick and only on a
// row's first insert, so an installation that added its domain after its hosts
// enrolled has no other way to attach them -- and a host with no domain publishes
// no VMs and can never be served over tls.
//
// Both configs that depend on the domain are pushed afterwards: the host's
// published names change, and so does whether it has a certificate to serve.
func (h *AdminHandler) SetClientDomain(c *echo.Context) error {
	id := c.Param("id")
	pgID, err := parseUUID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}

	var req setClientDomainReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// The zero pgtype.UUID is null, which is how the domain is cleared.
	var domainID pgtype.UUID
	if strings.TrimSpace(req.DomainID) != "" {
		domainID, err = parseUUID(req.DomainID)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid domain id")
		}
		if _, err := h.q.GetDomain(c.Request().Context(), domainID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return echo.NewHTTPError(http.StatusBadRequest, "unknown domain")
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "could not load the domain")
		}
	}

	if _, err := h.q.SetClientDomain(c.Request().Context(), db.SetClientDomainParams{
		ID: pgID, DomainID: domainID,
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not set the client's domain")
	}

	if h.certs != nil {
		h.certs.PushToClient(c.Request().Context(), pgID)
	}
	return c.NoContent(http.StatusNoContent)
}
