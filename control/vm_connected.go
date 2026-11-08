package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// Only handshakes that named a host are logged when allowed, so an address
// allowance is seen only when it carried web traffic.
const egressSeenQuery = `
SELECT lower(if(tls__sni != '', tls__sni, http__hostname)) AS host,
       toString(event_type)                               AS app,
       dest_ip, dest_port,
       toString(proto)                                    AS p,
       max(timestamp)                                     AS seen
FROM suricata_events
WHERE src_ip = toIPv6(?)
  AND timestamp >= ?
  AND event_type IN ('tls', 'http')
  AND (tls__sni != '' OR http__hostname != '')
GROUP BY host, app, dest_ip, dest_port, p
`

type egressSeen struct {
	host, appProto, proto string
	destIP                net.IP
	destPort              uint16
	at                    time.Time
}

type connectedHostDTO struct {
	Host     string `json:"host"`
	LastSeen string `json:"last_seen"`
}

type targetActivityDTO struct {
	TargetID      string             `json:"target_id"`
	LastConnected string             `json:"last_connected"`
	Hosts         []connectedHostDTO `json:"hosts"`
}

// @Summary     When each allowed destination was last used
// @Description One item per allowed destination. last_connected is empty when nothing has connected through it since it was added. hosts lists, for a domain, every name under it that was connected to, newest first; an address allowance has none, and is only seen when it carried http or tls.
// @Description
// @Description Like the denied list, only the last 7 days are read, or less if the VM is younger; window_seconds reports the window actually read. An empty last_connected means nothing within it.
// @Description
// @Description Read available first: when it is false nothing could be read, which says nothing about whether the destinations are in use.
// @Tags        egress
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} targetActivityList
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/connected [get]
func (h *UserHandler) ListTargetActivity(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	targets, err := h.q.ListVMNetworkTargets(ctx, vm.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list destinations")
	}
	since := deniedSince(vm, 0)
	window := int64(time.Since(since).Seconds())
	unavailable := map[string]any{"items": []targetActivityDTO{}, "available": false, "window_seconds": window}
	if h.ch == nil || vm.IP == "" {
		return c.JSON(http.StatusOK, unavailable)
	}
	seen, err := queryEgressSeen(ctx, h, vm.IP, since)
	if err != nil {
		log.Printf("could not read egress activity for %s: %v", vm.IP, err)
		return c.JSON(http.StatusOK, unavailable)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"items": targetActivity(targets, seen), "available": true, "window_seconds": window,
	})
}

func queryEgressSeen(ctx context.Context, h *UserHandler, ip string, since time.Time) ([]egressSeen, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := h.ch.Query(ctx, egressSeenQuery, ip, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []egressSeen
	for rows.Next() {
		var s egressSeen
		if err := rows.Scan(&s.host, &s.appProto, &s.destIP, &s.destPort, &s.proto, &s.at); err != nil {
			return nil, err
		}
		s.proto = strings.ToLower(s.proto)
		out = append(out, s)
	}
	return out, rows.Err()
}

func targetActivity(targets []db.VmNetworkTarget, seen []egressSeen) []targetActivityDTO {
	items := make([]targetActivityDTO, 0, len(targets))
	for _, t := range targets {
		dto := targetActivityDTO{TargetID: uuid.UUID(t.ID.Bytes).String(), Hosts: []connectedHostDTO{}}
		var last time.Time
		hosts := map[string]time.Time{}
		for _, s := range seen {
			if !t.CreatedAt.Valid || s.at.Before(t.CreatedAt.Time) || !seenMatches(t, s) {
				continue
			}
			if s.at.After(last) {
				last = s.at
			}
			if t.Kind == "domain" && s.at.After(hosts[s.host]) {
				hosts[s.host] = s.at
			}
		}
		if !last.IsZero() {
			dto.LastConnected = last.UTC().Format(time.RFC3339)
		}
		for host, at := range hosts {
			dto.Hosts = append(dto.Hosts, connectedHostDTO{Host: host, LastSeen: at.UTC().Format(time.RFC3339)})
		}
		sort.Slice(dto.Hosts, func(i, j int) bool {
			if dto.Hosts[i].LastSeen != dto.Hosts[j].LastSeen {
				return dto.Hosts[i].LastSeen > dto.Hosts[j].LastSeen
			}
			return dto.Hosts[i].Host < dto.Hosts[j].Host
		})
		items = append(items, dto)
	}
	return items
}

func seenMatches(t db.VmNetworkTarget, s egressSeen) bool {
	if t.Kind == "domain" {
		if s.host != t.Destination && !strings.HasSuffix(s.host, "."+t.Destination) {
			return false
		}
		wantTLS, wantHTTP := domainPorts(t.Ports)
		return (s.appProto == "tls" && wantTLS) || (s.appProto == "http" && wantHTTP)
	}
	if !addrMatches(t.Destination, s.destIP) {
		return false
	}
	switch t.Transport {
	case "icmp":
		return strings.HasPrefix(s.proto, "icmp") || s.proto == "ipv6-icmp"
	case "tcp", "udp":
		if s.proto != t.Transport {
			return false
		}
	case "any":
		if t.Ports != "" && s.proto != "tcp" && s.proto != "udp" {
			return false
		}
	}
	return portMatches(t.Ports, s.destPort)
}

func addrMatches(dest string, ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if _, n, err := net.ParseCIDR(dest); err == nil {
		return n.Contains(ip)
	}
	d := net.ParseIP(dest)
	return d != nil && d.Equal(ip)
}

func portMatches(ports string, port uint16) bool {
	if ports == "" {
		return true
	}
	for _, part := range strings.Split(ports, ",") {
		lo, hi, isRange := strings.Cut(part, ":")
		if !isRange {
			hi = lo
		}
		a, errA := strconv.Atoi(lo)
		b, errB := strconv.Atoi(hi)
		if errA == nil && errB == nil && int(port) >= a && int(port) <= b {
			return true
		}
	}
	return false
}
