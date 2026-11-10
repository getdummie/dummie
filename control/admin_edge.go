package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type setClientAdvertiseIPReq struct {
	AdvertiseIP string `json:"advertise_ip"`
}

func (h *AdminHandler) SetClientAdvertiseIP(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	var req setClientAdvertiseIPReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ip, err := validateAdvertiseIP(req.AdvertiseIP)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ctx := c.Request().Context()
	if _, err := h.q.SetClientAdvertiseIP(ctx, db.SetClientAdvertiseIPParams{ID: pgID, AdvertiseIP: ip}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such client")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not set the address")
	}
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, pgID)
	return c.NoContent(http.StatusNoContent)
}

type setDomainEdgeReq struct {
	EdgeClientID string `json:"edge_client_id"`
}

func (h *AdminHandler) SetDomainEdge(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid domain id")
	}
	var req setDomainEdgeReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()

	var edgeID pgtype.UUID
	if raw := strings.TrimSpace(req.EdgeClientID); raw != "" {
		edgeID, err = parseUUID(raw)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid edge id")
		}
		edge, err := h.q.GetClientByID(ctx, edgeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusBadRequest, "unknown client")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not read the client")
		}
		if edge.Role != clientRoleEdge || edge.Revoked {
			return echo.NewHTTPError(http.StatusBadRequest, "that client is not an edge; set role: edge in its dclient config")
		}
	}

	before, err := h.q.GetDomain(ctx, pgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "no such domain")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the domain")
	}
	if _, err := h.q.SetDomainEdge(ctx, db.SetDomainEdgeParams{ID: pgID, EdgeClientID: edgeID}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not set the domain's edge")
	}

	h.refreshDomainHosts(ctx, pgID)
	if before.EdgeClientID.Valid && before.EdgeClientID != edgeID {
		h.refreshEdge(ctx, before.EdgeClientID)
	}
	return c.NoContent(http.StatusNoContent)
}

// refreshDomainHosts re-pushes each host, which carries its current edge
// along with it.
func (h *AdminHandler) refreshDomainHosts(ctx context.Context, domainID pgtype.UUID) {
	ids, err := h.q.ListClientIDsByDomain(ctx, domainID)
	if err != nil {
		return
	}
	for _, id := range ids {
		pushProxyConfig(ctx, h.q, h.hub, h.proxy, id)
	}
}

// hostsBehindEdge is read before an edge goes away: afterwards nothing links
// the hosts to it, and they would keep trusting its key.
func (h *AdminHandler) hostsBehindEdge(ctx context.Context, edgeID pgtype.UUID) []pgtype.UUID {
	rows, err := h.q.ListEdgeHosts(ctx, edgeID)
	if err != nil {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func (h *AdminHandler) refreshHosts(ctx context.Context, ids []pgtype.UUID) {
	for _, id := range ids {
		pushProxyConfig(ctx, h.q, h.hub, h.proxy, id)
	}
}

func (h *AdminHandler) refreshEdge(ctx context.Context, edgeID pgtype.UUID) {
	if edge, err := h.q.GetClientByID(ctx, edgeID); err == nil {
		pushEdgeProxyConfig(ctx, h.q, h.hub, edge)
	}
}
