package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const (
	deniedWindow = 7 * 24 * time.Hour

	deniedLimit = 100
)

const deniedPacketsQuery = `
WITH flows AS (
    SELECT flow_id,
           any(dest_ip)   AS f_dest_ip,
           any(dest_port) AS f_dest_port,
           any(proto)     AS f_proto,
           argMax(app_proto, timestamp)        AS f_app_proto,
           argMax(alert__signature, timestamp) AS f_signature,
           max(timestamp)                      AS f_last_seen
    FROM suricata_events
    WHERE src_ip = toIPv6(?)
      AND timestamp >= ?
      AND alert__action = 'blocked'
    GROUP BY flow_id
),
names AS (
    SELECT flow_id, argMax(domain, timestamp) AS f_domain
    FROM suricata_events
    WHERE src_ip = toIPv6(?)
      AND timestamp >= ?
      AND domain != ''
    GROUP BY flow_id
)
SELECT f_dest_ip                       AS dest_ip,
       f_dest_port                     AS dest_port,
       f_proto                         AS proto,
       argMax(f_app_proto, f_last_seen) AS app_proto,
       max(f_domain)                   AS domain,
       count()                         AS attempts,
       max(f_last_seen)                AS last_seen,
       argMax(f_signature, f_last_seen) AS signature
FROM flows
LEFT JOIN names USING (flow_id)
GROUP BY f_dest_ip, f_dest_port, f_proto
ORDER BY last_seen DESC
LIMIT ?
`

const refusedLookupsQuery = `
SELECT qname AS domain, count() AS attempts, max(timestamp) AS last_seen
FROM dns_queries
WHERE src_ip = toIPv6(?)
  AND timestamp >= ?
  AND rcode = 'REFUSED'
  AND qname != ''
GROUP BY domain
ORDER BY last_seen DESC
LIMIT ?
`

const refusedSignature = "dclient: the resolver would not answer this name (not on the allowlist)"

type deniedAttemptDTO struct {
	Kind    string `json:"kind"`
	Domain  string `json:"domain"`
	Address string `json:"address"`
	Proto string `json:"proto"`
	Port  uint16 `json:"port"`
	AppProto  string `json:"app_proto"`
	Attempts  uint64 `json:"attempts"`
	LastSeen  string `json:"last_seen"`
	Signature string `json:"signature"`
}

// @Summary     List denied egress attempts
// @Description What this guest tried to reach and was refused. kind is "lookup" (a name the resolver would not answer) or "packet" (a flow the ruleset dropped); a refused lookup has no address to offer.
// @Description
// @Description Read available before reading items: an empty list means either nothing was denied or nothing is collecting, and those are very different facts. recording names which of the two sources answered.
// @Description
// @Description Only events since this VM was created are shown -- a destroyed VM's address goes back to the pool, so without that bound a new VM would inherit the history of whatever held its address before it.
// @Description
// @Description seconds narrows the window to the last n seconds, for watching what a run you just started is being denied. It can only narrow: the VM's creation and the 7 day ceiling still bound it, and window_seconds reports the window actually read.
// @Tags        egress
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Param       seconds query int false "look back only this many seconds" minimum(1)
// @Success     200 {object} deniedEgressList
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/denied [get]
func (h *UserHandler) ListDeniedEgress(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}
	v, err := h.q.GetVMForOwner(c.Request().Context(), db.GetVMForOwnerParams{
		ID: pgID, CreatedBy: owner,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
	}

	seconds, err := deniedWindowSeconds(c.QueryParam("seconds"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	since := deniedSince(v, seconds)
	if h.ch == nil || v.IP == "" {
		return c.JSON(http.StatusOK, map[string]any{
			"items":          []deniedAttemptDTO{},
			"available":      false,
			"recording":      deniedSources{},
			"window_seconds": int64(time.Since(since).Seconds()),
		})
	}

	items, sources := queryDeniedEgress(c.Request().Context(), h, v.IP, since)
	return c.JSON(http.StatusOK, map[string]any{
		"items": items,
		"available": sources.Packets || sources.Lookups,
		"recording":      sources,
		"window_seconds": int64(time.Since(since).Seconds()),
	})
}

func deniedWindowSeconds(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 0, errors.New("seconds must be a whole number of seconds, at least 1")
	}
	if n > int64(deniedWindow/time.Second) {
		return deniedWindow, nil
	}
	return time.Duration(n) * time.Second, nil
}

type deniedSources struct {
	Packets bool `json:"packets"`
	Lookups bool `json:"lookups"`
}

func deniedSince(v db.Vm, window time.Duration) time.Time {
	if window <= 0 || window > deniedWindow {
		window = deniedWindow
	}
	since := time.Now().Add(-window)
	if v.CreatedAt.Valid && v.CreatedAt.Time.After(since) {
		return v.CreatedAt.Time
	}
	return since
}

func queryDeniedEgress(ctx context.Context, h *UserHandler, ip string, since time.Time) ([]deniedAttemptDTO, deniedSources) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var sources deniedSources
	items := []deniedAttemptDTO{}

	packets, err := scanDeniedPackets(ctx, h, ip, since.UTC())
	if err != nil {
		log.Printf("could not read blocked packets for %s: %v", ip, err)
	} else {
		sources.Packets = true
		items = append(items, packets...)
	}

	lookups, err := scanRefusedLookups(ctx, h, ip, since.UTC())
	if err != nil {
		log.Printf("could not read refused lookups for %s (is 0002_dns_queries applied?): %v", ip, err)
	} else {
		sources.Lookups = true
		items = append(items, lookups...)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastSeen != items[j].LastSeen {
			return items[i].LastSeen > items[j].LastSeen
		}
		if items[i].Domain != items[j].Domain {
			return items[i].Domain < items[j].Domain
		}
		if items[i].Address != items[j].Address {
			return items[i].Address < items[j].Address
		}
		return items[i].Port < items[j].Port
	})
	if len(items) > deniedLimit {
		items = items[:deniedLimit]
	}
	return items, sources
}

func scanDeniedPackets(ctx context.Context, h *UserHandler, ip string, since time.Time) ([]deniedAttemptDTO, error) {
	rows, err := h.ch.Query(ctx, deniedPacketsQuery, ip, since, ip, since, deniedLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []deniedAttemptDTO{}
	for rows.Next() {
		var (
			destIP                          net.IP
			port                            uint16
			proto, appProto, domain, sigres string
			attempts                        uint64
			lastSeen                        time.Time
		)
		if err := rows.Scan(&destIP, &port, &proto, &appProto, &domain, &attempts, &lastSeen, &sigres); err != nil {
			return nil, err
		}
		items = append(items, deniedAttemptDTO{
			Kind:      "packet",
			Domain:    domain,
			Address:   displayIP(destIP),
			Proto:     strings.ToLower(proto),
			Port:      port,
			AppProto:  strings.ToLower(appProto),
			Attempts:  attempts,
			LastSeen:  lastSeen.UTC().Format(time.RFC3339),
			Signature: sigres,
		})
	}
	return items, rows.Err()
}

func scanRefusedLookups(ctx context.Context, h *UserHandler, ip string, since time.Time) ([]deniedAttemptDTO, error) {
	rows, err := h.ch.Query(ctx, refusedLookupsQuery, ip, since, deniedLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []deniedAttemptDTO{}
	for rows.Next() {
		var (
			domain   string
			attempts uint64
			lastSeen time.Time
		)
		if err := rows.Scan(&domain, &attempts, &lastSeen); err != nil {
			return nil, err
		}
		items = append(items, deniedAttemptDTO{
			Kind:      "lookup",
			Domain:    domain,
			Attempts:  attempts,
			LastSeen:  lastSeen.UTC().Format(time.RFC3339),
			Signature: refusedSignature,
		})
	}
	return items, rows.Err()
}

func displayIP(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}
