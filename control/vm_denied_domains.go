package main

import (
  "context"
  "errors"
  "log"
  "net"
  "net/http"
  "sort"
  "strings"
  "time"

  "github.com/jackc/pgx/v5"
  "github.com/labstack/echo/v5"

  "control/internal/db"
)

// This view answers one question an owner actually has: what did my VM try to do
// that policy stopped? It is read from clickhouse rather than postgres because
// nothing in the control plane's own tables records an attempt -- postgres holds
// what was *allowed*, and a denial is by definition something nobody wrote down in
// advance.
//
// It deliberately does not restrict itself to domains. A guest that cannot reach
// anything fails in whatever way its software happened to try: an ssh to a bare
// address, a ping, a package manager on a port nobody allowed. Showing only the
// attempts that carried a hostname meant the most confusing failures -- the ones
// with no name anywhere in them -- were the ones the page stayed silent about.
//
// There are two shapes of denial and they are recorded in two different places:
//
//   lookup  the resolver refused to answer a name. No packet was ever sent, so
//           suricata saw nothing at all; the record is a REFUSED in dns_queries.
//           This is the common case for anything addressed by name.
//
//   packet  the ruleset dropped a packet. suricata alerts on it, and the alert
//           carries the address, transport and port. If some other record on the
//           same flow carried a hostname -- a tls sni, an http host -- that is
//           joined back in, which is what puts a name on a blocked https request.
const (
  // deniedWindow bounds the scan. suricata_events' ORDER BY starts with
  // event_type, so a src_ip filter reads a lot of parts; without a time bound this
  // would get slower every day the fleet runs.
  deniedWindow = 7 * 24 * time.Hour

  deniedLimit = 100
)

// deniedPacketsQuery groups every dropped flow by where it was going.
//
// Grouped by destination rather than by flow: a guest retrying ssh forty times is
// one thing to allow or not, and forty rows of it is a list nobody reads. attempts
// counts flows, not packets, for the same reason -- a stalled tcp handshake
// retransmits on its own and that is not forty decisions.
//
// The join is the interesting part. An alert fires on a packet, and at SYN time
// that packet carries no name at all; a name arrives separately as a tls or http
// record for the same flow. So neither row alone is the whole answer and what ties
// them together is flow_id.
// The f_ prefixes on the subquery columns are load-bearing. Clickhouse resolves
// aliases eagerly, so a column named the same as an output alias -- selecting
// max(last_seen) AS last_seen while another aggregate on the same row reads
// last_seen -- resolves that read to the alias and fails with an aggregate function
// found inside an aggregate function. Naming the inner columns differently from the
// outer ones is what keeps the two apart.
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

// refusedLookupsQuery is the other half: names the resolver would not answer.
//
// No join and no flow, because there was no flow -- the guest asked, the gateway
// said no, and nothing was ever sent. rcode is the whole verdict.
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

// refusedSignature stands in for the rule name a packet denial carries. The column
// tells an owner what stopped the guest, and for a refusal the answer is always the
// same one.
const refusedSignature = "dagent: the resolver would not answer this name (not on the allowlist)"

// deniedAttemptDTO is one thing the guest tried and could not do.
//
// The fields the UI needs to offer an allowance are all here, which is the point:
// a row a user reads and then has to retype somewhere else is a row they will get
// wrong. Address, proto and port are what an address allowance is made of; domain
// is what a name allowance is made of; and a row can carry both, which is a blocked
// https request where the sni was seen.
type deniedAttemptDTO struct {
  // Kind is "lookup" or "packet" -- which of the two things above happened. The UI
  // switches on it, because a refused lookup has no address to offer.
  Kind    string `json:"kind"`
  Domain  string `json:"domain"`
  Address string `json:"address"`
  // Proto is lowercased for display: suricata writes TCP, and every other place a
  // transport appears in this system is lowercase.
  Proto string `json:"proto"`
  Port  uint16 `json:"port"`
  // AppProto is what suricata identified the traffic as, when it got far enough to
  // say -- "ssh", "ftp", "failed" when detection ran and found nothing. Empty for a
  // flow dropped at the syn, which is most of them.
  AppProto  string `json:"app_proto"`
  Attempts  uint64 `json:"attempts"`
  LastSeen  string `json:"last_seen"`
  Signature string `json:"signature"`
}

// ListDeniedEgress serves GET /api/v1/vms/:id/denied.
//
// Available reports whether the answer is trustworthy. An empty list means two very
// different things -- nothing was denied, or nothing is collecting -- and a screen
// that cannot tell them apart would quietly imply the first.
func (h *UserHandler) ListDeniedEgress(c *echo.Context) error {
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

  if h.ch == nil || v.IP == "" {
    return c.JSON(http.StatusOK, map[string]any{
      "items":     []deniedAttemptDTO{},
      "available": false,
      "recording": deniedSources{},
    })
  }

  items, sources := queryDeniedEgress(c.Request().Context(), h, v.IP, deniedSince(v))
  // Not a 500 when a source is unreadable: clickhouse being down says nothing about
  // the VM, and the rest of the page is fine.
  return c.JSON(http.StatusOK, map[string]any{
    "items": items,
    // True when at least one source answered. What the panel must never do is
    // present a partial list as a whole one, but going dark is not the way to avoid
    // that -- it hides denials that were recorded perfectly well and reports a
    // store-wide outage that is not happening. Which sources are live is the honest
    // answer, and the UI names the missing one.
    "available": sources.Packets || sources.Lookups,
    "recording": sources,
  })
}

// deniedSources says which of the two records could be read. They fail
// independently and for unrelated reasons -- suricata_events is written by vector
// from the host's eve.json, dns_queries by vector from the resolver's container log
// and needs its own migration -- so one being absent is normal and specific.
type deniedSources struct {
  Packets bool `json:"packets"`
  Lookups bool `json:"lookups"`
}

// deniedSince is the lower bound on the events this VM may be shown: whichever of
// the window and its own creation time is later.
//
// The creation half is not an optimisation, it is a correctness fix. A destroyed
// VM's address goes back to the pool and is handed to somebody else's guest, so
// without this a new VM inherits the history of whatever held its address before
// it -- attributed to the wrong owner, on a screen built for exactly one person to
// read.
func deniedSince(v db.Vm) time.Time {
  since := time.Now().Add(-deniedWindow)
  if v.CreatedAt.Valid && v.CreatedAt.Time.After(since) {
    return v.CreatedAt.Time
  }
  return since
}

// queryDeniedEgress reads both sources and merges them into one list, reporting
// which of them answered.
//
// A failure in one is not a failure of the call. The two are independent records of
// independent things, and the common case for the lookups half being unreadable is
// its migration not having been applied yet -- which is a reason to say so, not a
// reason to hide the packet denials that were recorded correctly.
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

  // Newest first. RFC 3339 in UTC sorts lexically, so the string comparison is the
  // chronological one. The remaining keys make the order total: a list that
  // reshuffled between two reads of an unchanged history would look like activity.
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

// displayIP renders an address the way a user would type it back in. The column is
// IPv6 so that one filter works across both tables, which means an IPv4 address
// comes back v4-mapped -- and "::ffff:5.75.175.235" is not something anyone would
// recognise as the host they just tried to ssh to.
func displayIP(ip net.IP) string {
  if ip == nil {
    return ""
  }
  if v4 := ip.To4(); v4 != nil {
    return v4.String()
  }
  return ip.String()
}
