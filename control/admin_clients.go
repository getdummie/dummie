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

type clientKeyDTO struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	KeyPrefix string `json:"key_prefix"`
	Uses      int32  `json:"uses"`
	MaxUses   *int32 `json:"max_uses"`
	ExpiresAt string `json:"expires_at"`
	Status    string `json:"status"`
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
	MaxUses        *int32 `json:"max_uses"`
	ExpiresInHours *int32 `json:"expires_in_hours"`
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

type clientDTO struct {
	ID            string `json:"id"`
	MachineID     string `json:"machine_id"`
	Hostname      string `json:"hostname"`
	Status        string `json:"status"`
	Connected     bool   `json:"connected"`
	OS            string `json:"os"`
	OSVersion     string `json:"os_version"`
	Arch          string `json:"arch"`
	ClientVersion string `json:"client_version"`
	LastSeenAt    string `json:"last_seen_at"`
	LastIP        string `json:"last_ip"`
	CreatedAt     string `json:"created_at"`
	Domain string `json:"domain"`
	DomainID string `json:"domain_id"`

	DclientVersion     string `json:"dclient_version"`
	DclientDownloadURL string `json:"dclient_download_url"`
	DpipeVersion       string `json:"dpipe_version"`
	DpipeDownloadURL   string `json:"dpipe_download_url"`
	ProxyVersion       string `json:"proxy_version"`
	ProxyDownloadURL   string `json:"proxy_download_url"`

	DpipeInstalledVersion string `json:"dpipe_installed_version"`
	ProxyInstalledVersion string `json:"proxy_installed_version"`

	Metrics clientMetricsDTO `json:"metrics"`
}

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

		DclientVersion:     a.DclientVersion,
		DclientDownloadURL: a.DclientDownloadURL,
		DpipeVersion:       a.DpipeVersion,
		DpipeDownloadURL:   a.DpipeDownloadURL,
		ProxyVersion:       a.ProxyVersion,
		ProxyDownloadURL:   a.ProxyDownloadURL,

		DpipeInstalledVersion: a.DpipeInstalledVersion,
		ProxyInstalledVersion: a.ProxyInstalledVersion,

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

type setClientDomainReq struct {
	DomainID string `json:"domain_id"`
}

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

type updateClientServicesReq struct {
	DclientVersion     string `json:"dclient_version"`
	DclientDownloadURL string `json:"dclient_download_url"`
	DpipeVersion       string `json:"dpipe_version"`
	DpipeDownloadURL   string `json:"dpipe_download_url"`
	ProxyVersion       string `json:"proxy_version"`
	ProxyDownloadURL   string `json:"proxy_download_url"`
}

func (h *AdminHandler) UpdateClientServices(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}

	var req updateClientServicesReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	params := db.UpdateClientServiceVersionsParams{
		ID:                 pgID,
		DclientVersion:     strings.TrimSpace(req.DclientVersion),
		DclientDownloadURL: strings.TrimSpace(req.DclientDownloadURL),
		DpipeVersion:       strings.TrimSpace(req.DpipeVersion),
		DpipeDownloadURL:   strings.TrimSpace(req.DpipeDownloadURL),
		ProxyVersion:       strings.TrimSpace(req.ProxyVersion),
		ProxyDownloadURL:   strings.TrimSpace(req.ProxyDownloadURL),
	}
	for _, f := range []struct {
		label   string
		version string
		url     string
	}{
		{"dclient", params.DclientVersion, params.DclientDownloadURL},
		{"dpipe", params.DpipeVersion, params.DpipeDownloadURL},
		{"dproxy", params.ProxyVersion, params.ProxyDownloadURL},
	} {
		if f.version != "" {
			if err := validateReleaseVersion(f.version); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, f.label+" version "+err.Error())
			}
		}
		if err := validateServiceDownloadURL(f.url); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, f.label+" download url "+err.Error())
		}
	}

	client, err := h.q.UpdateClientServiceVersions(c.Request().Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "no such client")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the client's versions")
	}

	pushServicesConfig(c.Request().Context(), h.q, h.hub, client, false)

	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) UpgradeClientServices(c *echo.Context) error {
	id := c.Param("id")
	pgID, err := parseUUID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	if !h.hub.Connected(id) {
		return echo.NewHTTPError(http.StatusConflict, "this client is not connected, so it cannot be upgraded right now")
	}

	client, err := h.q.GetClientByID(c.Request().Context(), pgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such client")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read client")
	}

	pushServicesConfig(c.Request().Context(), h.q, h.hub, client, true)
	return c.NoContent(http.StatusNoContent)
}
