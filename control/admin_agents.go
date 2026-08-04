package main

import (
  "net/http"
  "strings"
  "time"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5/pgtype"
  "github.com/labstack/echo/v5"

  "control/internal/db"
)

// --- enrollment keys --------------------------------------------------------

type agentKeyDTO struct {
  ID        string `json:"id"`
  Label     string `json:"label"`
  KeyPrefix string `json:"key_prefix"`
  Uses      int32  `json:"uses"`
  MaxUses   *int32 `json:"max_uses"`   // null = unlimited
  ExpiresAt string `json:"expires_at"` // "" = never
  Status    string `json:"status"`     // active | revoked | expired | exhausted
  CreatedAt string `json:"created_at"`
}

func toAgentKeyDTO(k db.AgentEnrollmentKey) agentKeyDTO {
  d := agentKeyDTO{
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

func (h *AdminHandler) ListAgentKeys(c *echo.Context) error {
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
  items := make([]agentKeyDTO, 0, len(rows))
  for _, k := range rows {
    items = append(items, toAgentKeyDTO(k))
  }
  return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

type createAgentKeyReq struct {
  Label          string `json:"label"`
  MaxUses        *int32 `json:"max_uses"`         // null/omitted = unlimited
  ExpiresInHours *int32 `json:"expires_in_hours"` // null/omitted = never
}

func (h *AdminHandler) CreateAgentKey(c *echo.Context) error {
  var req createAgentKeyReq
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
    "item": toAgentKeyDTO(k),
  })
}

func (h *AdminHandler) RevokeAgentKey(c *echo.Context) error {
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid key id")
  }
  if err := h.q.RevokeEnrollmentKey(c.Request().Context(), pgID); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not revoke key")
  }
  return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) DeleteAgentKey(c *echo.Context) error {
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid key id")
  }
  if err := h.q.DeleteEnrollmentKey(c.Request().Context(), pgID); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not delete key")
  }
  return c.NoContent(http.StatusNoContent)
}

// --- agents -----------------------------------------------------------------

type agentDTO struct {
  ID           string `json:"id"`
  MachineID    string `json:"machine_id"`
  Hostname     string `json:"hostname"`
  Status       string `json:"status"` // online | offline | revoked
  Connected    bool   `json:"connected"`
  OS           string `json:"os"`
  OSVersion    string `json:"os_version"`
  Arch         string `json:"arch"`
  AgentVersion string `json:"agent_version"`
  LastSeenAt   string `json:"last_seen_at"`
  LastIP       string `json:"last_ip"`
  CreatedAt    string `json:"created_at"`

  Metrics agentMetricsDTO `json:"metrics"`
}

// agentMetricsDTO is the host snapshot the agent last reported. ReportedAt is
// "" when it never has -- the numbers below it are zero either way, so this is
// the only thing that distinguishes an idle host from a silent one.
type agentMetricsDTO struct {
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

func toAgentDTO(a db.Agent, connected bool) agentDTO {
  d := agentDTO{
    ID:           uuid.UUID(a.ID.Bytes).String(),
    MachineID:    a.MachineID,
    Hostname:     a.Hostname,
    Status:       a.Status,
    Connected:    connected,
    OS:           a.OS,
    OSVersion:    a.OSVersion,
    Arch:         a.Arch,
    AgentVersion: a.AgentVersion,
    LastIP:       a.LastIP,
    CreatedAt:    a.CreatedAt.Time.Format(time.RFC3339),
    Metrics: agentMetricsDTO{
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

func (h *AdminHandler) ListAgents(c *echo.Context) error {
  ctx := c.Request().Context()
  limit, offset := pageParams(c)
  total, err := h.q.CountAgents(ctx)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not count agents")
  }
  rows, err := h.q.ListAgents(ctx, db.ListAgentsParams{Limit: limit, Offset: offset})
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not list agents")
  }
  items := make([]agentDTO, 0, len(rows))
  for _, a := range rows {
    id := uuid.UUID(a.ID.Bytes).String()
    items = append(items, toAgentDTO(a, h.hub.Connected(id)))
  }
  return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

// RevokeAgent invalidates the agent's token and kicks its live socket, so the
// connection cannot outlive its authorization.
func (h *AdminHandler) RevokeAgent(c *echo.Context) error {
  id := c.Param("id")
  pgID, err := parseUUID(id)
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid agent id")
  }
  if err := h.q.RevokeAgent(c.Request().Context(), pgID); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not revoke agent")
  }
  h.hub.Kick(id, "agent revoked")
  return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) DeleteAgent(c *echo.Context) error {
  id := c.Param("id")
  pgID, err := parseUUID(id)
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid agent id")
  }
  if err := h.q.DeleteAgent(c.Request().Context(), pgID); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not delete agent")
  }
  h.hub.Kick(id, "agent deleted")
  return c.NoContent(http.StatusNoContent)
}
