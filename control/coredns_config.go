package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
	"control/internal/proto"
)

const corednsRefuseAll = `.:53 {
    template ANY ANY {
        rcode REFUSED
    }
` + corednsLogDirective + `    errors
}
`

const corednsLogMarker = "dclientdns"

const corednsLogDirective = `    log . "` + corednsLogMarker + ` {remote} {rcode} {type} {name}" {
        class all
    }
`

const corednsHeader = `# Written by the control server and installed by dclient. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
#
# Every guest on this host is pointed here by dhcp, and this file is the whole
# list of names any of them may resolve. A guest with no allowances gets no block
# of its own and falls through to the refuse-everything server at the bottom, and
# a guest allowed the whole address space gets a block that forwards every name.
#
# Blocks are matched by zone first and then by view, and coredns consults the
# blocks that have a view before the ones that do not -- which is what makes the
# last block a fallback rather than a competitor.
`

func generateCoreDNSConfig(rows []db.ListVMNetworkTargetsByClientRow, upstream, tld string) string {
	var b strings.Builder
	b.WriteString(corednsHeader)
	writeCoreDNSIntegrationZone(&b, tld)

	for _, vm := range groupByVM(rows) {
		if vm.allowAll {
			fmt.Fprintf(&b, "\n# --- %s (%s) at %s may resolve anything (%s is allowed) ---\n",
				corefileText(vm.name), corefileText(vm.hostID), vm.ip, targetEverywhere)
			fmt.Fprintf(&b, ".:53 {\n    view %s {\n        expr client_ip() == '%s'\n    }\n",
				viewName(vm.ip, "allow"), vm.ip)
			fmt.Fprintf(&b, "    forward . %s\n", upstream)
			b.WriteString("    cache 30\n")
			b.WriteString(corednsLogDirective)
			b.WriteString("    errors\n}\n")
			continue
		}

		zones := vmZones(vm)
		if len(zones) == 0 {
			fmt.Fprintf(&b, "\n# %s (%s) at %s may resolve nothing\n",
				corefileText(vm.name), corefileText(vm.hostID), vm.ip)
			continue
		}

		fmt.Fprintf(&b, "\n# --- %s (%s) at %s ---\n",
			corefileText(vm.name), corefileText(vm.hostID), vm.ip)

		fmt.Fprintf(&b, "%s:53 {\n", strings.Join(zones, ":53 "))
		fmt.Fprintf(&b, "    view %s {\n        expr client_ip() == '%s'\n    }\n", viewName(vm.ip, "allow"), vm.ip)
		fmt.Fprintf(&b, "    forward . %s\n", upstream)
		b.WriteString("    cache 30\n")
		b.WriteString(corednsLogDirective)
		b.WriteString("    errors\n}\n")

		fmt.Fprintf(&b, ".:53 {\n    view %s {\n        expr client_ip() == '%s'\n    }\n",
			viewName(vm.ip, "deny"), vm.ip)
		b.WriteString("    template ANY ANY {\n        rcode REFUSED\n    }\n")
		b.WriteString(corednsLogDirective)
		b.WriteString("    errors\n}\n")
	}

	b.WriteString("\n# --- everything else ------------------------------------------------------\n")
	b.WriteString("# Both the guests with no allowances and any client that is not a guest.\n")
	b.WriteString(corednsRefuseAll)
	return b.String()
}

// writeCoreDNSIntegrationZone answers *.int.<tld> for every guest. It carries no
// view on purpose: resolving one of these names grants nothing, because intproxy
// still asks the control server whether the calling vm is attached.
//
// It sits above the per-vm blocks, but placement is not what makes it win --
// int.<tld> is a longer zone match than the .:53 blocks, and zone beats view.
func writeCoreDNSIntegrationZone(b *strings.Builder, tld string) {
	if tld == "" {
		return
	}
	zone := proxyIntLabel + "." + tld
	fmt.Fprintf(b, "\n# --- %s: this host's integration proxy ---\n", zone)
	fmt.Fprintf(b, "%s:53 {\n", zone)
	fmt.Fprintf(b, "    template IN A %s {\n", zone)
	fmt.Fprintf(b, "        answer \"{{ .Name }} 60 IN A %s\"\n", intproxyAddr)
	b.WriteString("    }\n")
	// An empty NOERROR rather than no block at all: a dual-stack guest that gets
	// REFUSED for AAAA can stall instead of falling back to A.
	fmt.Fprintf(b, "    template IN AAAA %s {\n        rcode NOERROR\n    }\n", zone)
	b.WriteString(corednsLogDirective)
	b.WriteString("    errors\n}\n")
}

func vmZones(vm vmTargets) []string {
	seen := map[string]bool{}
	var zones []string
	for _, r := range vm.targets {
		if r.Kind != "domain" || r.Destination == "" || seen[r.Destination] {
			continue
		}
		seen[r.Destination] = true
		zones = append(zones, r.Destination)
	}
	sort.Strings(zones)
	return zones
}

func viewName(ip, suffix string) string {
	return "vm_" + strings.NewReplacer(".", "_", ":", "_").Replace(ip) + "_" + suffix
}

var corefileTextReplacer = strings.NewReplacer("\n", " ", "\r", " ")

func corefileText(s string) string {
	out := strings.TrimSpace(corefileTextReplacer.Replace(s))
	if out == "" {
		return "unnamed"
	}
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

func pushCoreDNSConfigToAll(ctx context.Context, q *db.Queries, hub *Hub) {
	ids := hub.ConnectedIDs()
	sent := 0
	for _, id := range ids {
		clientID, err := parseUUID(id)
		if err != nil {
			log.Printf("connected client %q does not have a usable id, so no corefile was sent", id)
			continue
		}
		pushCoreDNSConfig(ctx, q, hub, clientID)
		sent++
	}
	if sent > 0 {
		log.Printf("pushed a corefile to %d connected client(s)", sent)
	}
}

func pushCoreDNSConfig(ctx context.Context, q *db.Queries, hub *Hub, clientID pgtype.UUID) {
	id := uuid.UUID(clientID.Bytes).String()
	rows, err := q.ListVMNetworkTargetsByClient(ctx, clientID)
	if err != nil {
		log.Printf("could not read the allowed destinations for client %s: %v", id, err)
		return
	}
	upstream := setting(ctx, q, settingResolverUpstream)
	if upstream == "" {
		log.Printf("no resolver upstream is set, so no corefile was sent to client %s", id)
		return
	}
	host, err := clientProxyHost(ctx, q, clientID)
	if err != nil {
		log.Printf("could not read the domain of client %s, writing its corefile without an integration zone: %v", id, err)
	}
	env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
		Kind: proto.KindCoreDNSConfig,
		File: &proto.FileConfig{Config: generateCoreDNSConfig(rows, upstream, host.tld)},
	})
	if err != nil {
		log.Printf("could not build the corefile job for client %s: %v", id, err)
		return
	}
	if err := hub.Send(id, env); err != nil {
		log.Printf("could not deliver the corefile to client %s: %v", id, err)
	}
}
