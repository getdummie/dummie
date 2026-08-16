package main

import (
  "context"
  "errors"
  "log"
  "net/http"
  "time"

  "github.com/jackc/pgx/v5"
  "github.com/labstack/echo/v5"

  "control/internal/db"
)

// The denied-domains view answers one question an owner actually has: what did
// my VM try to reach that policy stopped? It is read from clickhouse rather
// than postgres because nothing in the control plane's own tables records an
// attempt -- postgres holds what was *allowed*, and a denial is by definition
// something nobody wrote down in advance.
//
// The join is the interesting part. A blocked packet and the name it was going
// to are two different rows: suricata alerts on the packet, which at SYN time
// carries no name at all, and emits the name separately as a dns/tls/http
// record for the same flow. So neither row alone answers the question, and what
// ties them together is flow_id.
const (
  // deniedDomainsWindow bounds the scan. The table's ORDER BY starts with
  // event_type, so a src_ip filter reads a lot of parts; without a time bound
  // this would get slower every day the fleet runs.
  deniedDomainsWindow = 7 * 24 * time.Hour

  deniedDomainsLimit = 100
)

// deniedDomainsQuery groups every named destination on a flow something blocked.
//
// `blocked` is the set of flows that were denied, with the rule that did it.
// The outer select then takes every row on those flows that carries a name --
// dns rrname, tls sni or http host, which is what the `domain` materialized
// column coalesces -- and counts the attempts per name.
const deniedDomainsQuery = `
WITH blocked AS (
    SELECT flow_id, argMax(alert__signature, timestamp) AS signature
    FROM suricata_events
    WHERE src_ip = toIPv6(?)
      AND timestamp >= now() - INTERVAL ? SECOND
      AND alert__action = 'blocked'
    GROUP BY flow_id
)
SELECT
    e.domain AS domain,
    count() AS attempts,
    max(e.timestamp) AS last_seen,
    argMax(b.signature, e.timestamp) AS signature
FROM suricata_events AS e
INNER JOIN blocked AS b ON e.flow_id = b.flow_id
WHERE e.src_ip = toIPv6(?)
  AND e.timestamp >= now() - INTERVAL ? SECOND
  AND e.domain != ''
GROUP BY domain
ORDER BY last_seen DESC
LIMIT ?
`

type deniedDomainDTO struct {
  Domain   string `json:"domain"`
  Attempts uint64 `json:"attempts"`
  LastSeen string `json:"last_seen"`
  // Signature is the rule that denied it, which is what tells an owner whether
  // the answer is "add this destination" or "this port is never allowed".
  Signature string `json:"signature"`
}

// ListDeniedDomains serves GET /api/v1/vms/:id/denied-domains.
//
// Available reports whether the answer is trustworthy. An empty list means two
// very different things -- nothing was denied, or nothing is collecting events
// -- and a screen that cannot tell them apart would quietly imply the first.
func (h *UserHandler) ListDeniedDomains(c *echo.Context) error {
  owner, err := callerID(c)
  if err != nil {
    return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }
  // Scoped to the caller like every other route here: this reads one guest's
  // browsing history, so someone else's VM has to be a 404 rather than a query.
  v, err := h.q.GetVMForOwner(c.Request().Context(), db.GetVMForOwnerParams{
    ID: pgID, CreatedBy: owner,
  })
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusNotFound, "no such vm")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
  }

  empty := map[string]any{"items": []deniedDomainDTO{}, "available": false}
  if h.ch == nil || v.IP == "" {
    return c.JSON(http.StatusOK, empty)
  }

  items, err := queryDeniedDomains(c.Request().Context(), h, v.IP)
  if err != nil {
    // Not a 500: clickhouse being down says nothing about the VM, and the rest
    // of the page is fine. The panel says it could not read rather than
    // claiming there were no denials.
    log.Printf("could not read denied domains for %s: %v", v.IP, err)
    return c.JSON(http.StatusOK, empty)
  }
  return c.JSON(http.StatusOK, map[string]any{"items": items, "available": true})
}

func queryDeniedDomains(ctx context.Context, h *UserHandler, ip string) ([]deniedDomainDTO, error) {
  ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
  defer cancel()

  window := int64(deniedDomainsWindow / time.Second)
  rows, err := h.ch.Query(ctx, deniedDomainsQuery, ip, window, ip, window, deniedDomainsLimit)
  if err != nil {
    return nil, err
  }
  defer rows.Close()

  items := []deniedDomainDTO{}
  for rows.Next() {
    var (
      domain, signature string
      attempts          uint64
      lastSeen          time.Time
    )
    if err := rows.Scan(&domain, &attempts, &lastSeen, &signature); err != nil {
      return nil, err
    }
    items = append(items, deniedDomainDTO{
      Domain:    domain,
      Attempts:  attempts,
      LastSeen:  lastSeen.UTC().Format(time.RFC3339),
      Signature: signature,
    })
  }
  return items, rows.Err()
}
