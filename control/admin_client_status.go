package main

import (
	"log"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"control/internal/proto"
)

// The host gives its checks 6s, so this leaves room for the reply.
const statusJobWait = 10 * time.Second

func (h *AdminHandler) GetClientStatus(c *echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if _, err := parseUUID(id); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	if !h.hub.Connected(id) {
		return echo.NewHTTPError(http.StatusConflict, "this host is not connected, so its services cannot be checked")
	}

	res, err := h.hub.Ask(ctx, id, proto.Job{Kind: proto.KindHostStatus}, statusJobWait)
	if err != nil {
		log.Printf("client %s: could not read the service status: %v", id, err)
		return echo.NewHTTPError(http.StatusBadGateway, "the host did not report its service status")
	}
	if !res.OK {
		msg := res.Error
		if msg == "" {
			msg = "the host could not check its services"
		}
		return echo.NewHTTPError(http.StatusBadGateway, msg)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"items":       emptySlice(res.Status),
		"reported_at": time.Now().UTC().Format(time.RFC3339),
	})
}
