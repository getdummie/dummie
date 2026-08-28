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

	// Which build of each managed binary this host is meant to run. A version is
	// a bare release number the host turns into a github release URL; the URL
	// beside it overrides that when set, which is how a custom build gets onto one
	// machine. Both "" means "track this control server's own version", so the
	// screen shows them empty rather than inventing a value the row does not hold.
	//
	// ClientVersion above is the other half of the pair: this is what the host was
	// told to run, that is what it reported actually running.
	DclientVersion     string `json:"dclient_version"`
	DclientDownloadURL string `json:"dclient_download_url"`
	DpipeVersion       string `json:"dpipe_version"`
	DpipeDownloadURL   string `json:"dpipe_download_url"`
	ProxyVersion       string `json:"proxy_version"`
	ProxyDownloadURL   string `json:"proxy_download_url"`

	// What the host reports it actually has, read off the binaries themselves. ""
	// when it has not said -- either the binary is not there or it could not be
	// asked -- which the screen shows as a dash. dclient's own is ClientVersion
	// above, since it reports its own build rather than one it manages.
	DpipeInstalledVersion string `json:"dpipe_installed_version"`
	ProxyInstalledVersion string `json:"proxy_installed_version"`

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

// updateClientServicesReq names the build of each managed binary a host should
// run. An empty version means "track this control server's version"; an empty url
// means "build it from the version".
type updateClientServicesReq struct {
	DclientVersion     string `json:"dclient_version"`
	DclientDownloadURL string `json:"dclient_download_url"`
	DpipeVersion       string `json:"dpipe_version"`
	DpipeDownloadURL   string `json:"dpipe_download_url"`
	ProxyVersion       string `json:"proxy_version"`
	ProxyDownloadURL   string `json:"proxy_download_url"`
}

// UpdateClientServices records which builds a host should run. It does not move
// a host that is already running something: that is UpgradeClientServices, behind
// a button and a confirmation, because replacing these binaries drops every ssh
// session on the machine and restarts its control plane.
//
// The push it does send is unforced, so it installs anything the host is missing
// and changes nothing else. That is worth sending now rather than at the next
// connect: a freshly enrolled host with nothing on it should not wait.
//
// All six fields are written together because they are one decision: the three
// binaries are cut from the same release, and a host part-way between two of them
// is a combination nobody tested. A request that omits a field clears it, which is
// how a host goes back to tracking this server's version.
//
// Every value is validated before it is stored, not just before it is sent. These
// end up as URLs whose contents each host installs and runs as root, and the row
// is read again on every connect -- so a value that was never checked would be a
// standing instruction rather than a one-off mistake.
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
		// An empty version is allowed -- that is how a host goes back to this
		// server's own -- but a value that is there has to be a release number.
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

	// No body, like SetClientDomain: the row this returns carries no domain, and
	// rendering a DTO without one would tell the screen the host lost its domain.
	// The caller re-reads the client instead.
	return c.NoContent(http.StatusNoContent)
}

// UpgradeClientServices moves one host onto the builds recorded against it.
//
// Separate from saving them, and the only thing in the control plane that
// replaces a binary already running on a host. Everything else about this feature
// converges quietly; this is the one action with a cost -- dproxy comes back with
// new listeners, dpipe hands its sessions over, and dclient replaces itself and
// restarts, so the host goes offline for a few seconds.
//
// Refused when the host is not connected rather than queued. A queued upgrade
// would fire at whatever hour the machine next came back, which is not a thing
// anyone asked for, and the fields are already saved -- so pressing this again
// when it is up costs nothing.
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
