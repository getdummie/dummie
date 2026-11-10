package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
	"control/internal/proto"
)

const (
	clientRoleHost = "host"
	clientRoleEdge = "edge"
)

const errEdgeRunsNoVMs = "that client is an edge: it routes to hosts and runs no vms"

// settingDpipeEdgeClientKey is apart from the fleet client key, which every
// host holds, so a host cannot pass itself off as the edge to another host.
const settingDpipeEdgeClientKey = "dpipe_edge_ssh_client_key"

func helloRole(role string) string {
	if role == clientRoleEdge {
		return clientRoleEdge
	}
	return clientRoleHost
}

func validateAdvertiseIP(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return "", errors.New("must be an ip address, or empty to use the one the client reports")
	}
	return ip.String(), nil
}

// isEdgeClient skips the host-only configs: an edge has no guests to resolve
// names for, inspect traffic of, or proxy integrations to.
func isEdgeClient(ctx context.Context, q *db.Queries, clientID pgtype.UUID) bool {
	c, err := q.GetClientByID(ctx, clientID)
	return err == nil && c.Role == clientRoleEdge
}

func clientAddress(c db.Client) string {
	if c.AdvertiseIP != "" {
		return c.AdvertiseIP
	}
	return c.ReportedIP
}

func hostEdge(ctx context.Context, q *db.Queries, hostID pgtype.UUID) (db.Client, bool) {
	edge, err := q.GetHostEdge(ctx, hostID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("could not read the edge of client %s: %v", uuid.UUID(hostID.Bytes).String(), err)
		}
		return db.Client{}, false
	}
	return edge, true
}

type edgeUpstream struct {
	pubkey string
	source string
}

func hostUpstream(ctx context.Context, q *db.Queries, hostID pgtype.UUID) *edgeUpstream {
	edge, ok := hostEdge(ctx, q, hostID)
	if !ok {
		return nil
	}
	source := clientAddress(edge)
	if source == "" {
		log.Printf("edge %s has no address yet, so its hosts do not trust it", uuid.UUID(edge.ID.Bytes).String())
		return nil
	}
	_, pub, err := fleetSSHKey(ctx, q, settingDpipeEdgeClientKey, "dpipe-edge")
	if err != nil {
		log.Printf("could not issue the edge ssh key: %v", err)
		return nil
	}
	return &edgeUpstream{pubkey: strings.TrimSpace(pub), source: source}
}

type edgeRoutes struct {
	hosts    [][2]string
	fallback map[string][]string
	tlds     []string
	ssh      []db.ListEdgeSSHUsersRow
}

func loadEdgeRoutes(ctx context.Context, q *db.Queries, edgeID pgtype.UUID) (edgeRoutes, error) {
	out := edgeRoutes{fallback: map[string][]string{}}

	hosts, err := q.ListEdgeHosts(ctx, edgeID)
	if err != nil {
		return out, fmt.Errorf("could not list the hosts: %w", err)
	}
	for _, h := range hosts {
		if h.Address == "" {
			continue
		}
		if _, seen := out.fallback[h.TLD]; !seen {
			out.tlds = append(out.tlds, h.TLD)
		}
		out.fallback[h.TLD] = append(out.fallback[h.TLD], h.Address)
	}

	vms, err := q.ListEdgeHTTPRoutes(ctx, edgeID)
	if err != nil {
		return out, fmt.Errorf("could not list the vms: %w", err)
	}
	for _, v := range vms {
		if v.Address != "" {
			out.hosts = append(out.hosts, [2]string{v.VMName + "." + v.DomainTLD, v.Address})
		}
	}

	custom, err := q.ListEdgeCustomDomainRoutes(ctx, edgeID)
	if err != nil {
		return out, fmt.Errorf("could not list the custom domains: %w", err)
	}
	for _, c := range custom {
		if c.Address != "" {
			out.hosts = append(out.hosts, [2]string{c.Domain, c.Address})
		}
	}

	users, err := q.ListEdgeSSHUsers(ctx, edgeID)
	if err != nil {
		return out, fmt.Errorf("could not list the ssh users: %w", err)
	}
	for _, u := range users {
		if u.Address != "" {
			out.ssh = append(out.ssh, u)
		}
	}
	return out, nil
}

const edgeProxyHeader = `# Written by the control server and installed by dclient. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
#
# This client is an edge. It serves no vms: http and https are forwarded
# untouched, by Host or SNI, to the host serving the name, and ssh is checked
# against the person's key here, then relayed to that host as <vm>@host.
`

func generateEdgeProxyConfig(r edgeRoutes) string {
	var b strings.Builder
	b.WriteString(edgeProxyHeader)
	b.WriteString(proxyStaticConfig)

	b.WriteString("\nhttp:\n")
	fmt.Fprintf(&b, "  listen: %q\n", proxyHTTPListen)
	b.WriteString("  reuseport: true\n")
	b.WriteString("  hosts: {}\n")
	b.WriteString("\nhttps:\n")
	fmt.Fprintf(&b, "  listen: %q\n", proxyHTTPSListen)
	b.WriteString("  reuseport: true\n")

	b.WriteString("\nedge:\n")
	if len(r.hosts) == 0 {
		b.WriteString("  hosts: {}\n")
	} else {
		b.WriteString("  hosts:\n")
		for _, h := range r.hosts {
			if !hostnamePattern.MatchString(h[0]) {
				log.Printf("skipping the edge route for %q: not a usable hostname", h[0])
				continue
			}
			fmt.Fprintf(&b, "    %s: %s\n", h[0], yamlString(h[1]))
		}
	}
	if len(r.tlds) == 0 {
		b.WriteString("  fallback: {}\n")
	} else {
		b.WriteString("  fallback:\n")
		for _, tld := range r.tlds {
			fmt.Fprintf(&b, "    %s:\n", tld)
			for _, addr := range r.fallback[tld] {
				fmt.Fprintf(&b, "      - %s\n", yamlString(addr))
			}
		}
	}

	if len(r.ssh) > 0 {
		b.WriteString("\nssh:\n")
		fmt.Fprintf(&b, "  listen: %q\n", proxySSHListen)
		b.WriteString("  reuseport: true\n")
		b.WriteString("  users:\n")
		for _, u := range r.ssh {
			fmt.Fprintf(&b, "    - pubkey: %s\n", yamlString(u.PublicKey))
			fmt.Fprintf(&b, "      vm_name: %s\n", yamlString(u.VMName))
			fmt.Fprintf(&b, "      target: %s\n", yamlString(net.JoinHostPort(u.Address, "22")))
			fmt.Fprintf(&b, "      remote_user: %s\n", yamlString(u.VMName))
		}
	}

	b.WriteString(proxyConfigFooter)
	return b.String()
}

func pushEdgeProxyConfig(ctx context.Context, q *db.Queries, hub *Hub, edge db.Client) {
	id := uuid.UUID(edge.ID.Bytes).String()
	if !hub.Connected(id) {
		return
	}
	routes, err := loadEdgeRoutes(ctx, q, edge.ID)
	if err != nil {
		log.Printf("edge %s: %v", id, err)
		return
	}
	env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
		Kind:  proto.KindProxyConfig,
		Proxy: &proto.ProxyConfig{Config: generateEdgeProxyConfig(routes)},
	})
	if err != nil {
		log.Printf("could not build the proxy config job for edge %s: %v", id, err)
		return
	}
	if err := hub.Send(id, env); err != nil {
		log.Printf("could not deliver the proxy config to edge %s: %v", id, err)
	}
}

const edgeDpipeConfig = `# Written by the control server and installed by dclient. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
control_socket: /run/dpipe/control.sock
upgrade_socket: /run/dpipe/upgrade.sock
drain_timeout: 0s
log_level: info

# An edge serves the fleet's host key, so a person's known_hosts is the same
# with or without it, and logs in to the hosts with a key only edges hold.
ssh:
  enabled: true
  host_key: /etc/dpipe/keys/dpipe_host_ed25519
  client_key: /etc/dpipe/keys/dpipe_client_ed25519
  backend_known_hosts: /etc/dpipe/keys/backend_known_hosts
  dial_timeout: 5s
  resolve_timeout: 3s

tls:
  enabled: false
`

func pushEdgeDpipeConfig(ctx context.Context, q *db.Queries, hub *Hub, edgeID pgtype.UUID) {
	id := uuid.UUID(edgeID.Bytes).String()
	host, hostPub, err := fleetSSHKey(ctx, q, settingDpipeHostKey, "dpipe-host")
	if err != nil {
		log.Printf("could not issue the dpipe host key for edge %s: %v", id, err)
		return
	}
	client, clientPub, err := fleetSSHKey(ctx, q, settingDpipeEdgeClientKey, "dpipe-edge")
	if err != nil {
		log.Printf("could not issue the edge ssh key for edge %s: %v", id, err)
		return
	}
	env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
		Kind: proto.KindDpipeConfig,
		File: &proto.FileConfig{Config: edgeDpipeConfig},
		DpipeKeys: &proto.DpipeSSHKeys{
			HostKey: host, HostPub: hostPub,
			ClientKey: client, ClientPub: clientPub,
			KnownHosts: "* " + strings.TrimSpace(hostPub) + "\n",
		},
	})
	if err != nil {
		log.Printf("could not build the dpipe config job for edge %s: %v", id, err)
		return
	}
	if err := hub.Send(id, env); err != nil {
		log.Printf("could not deliver the dpipe config to edge %s: %v", id, err)
	}
}
