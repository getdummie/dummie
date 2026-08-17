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

// The Corefile is the other half of the egress policy, and it is compiled here
// from the same rows as local.rules for one reason: the two files have to agree
// about what a domain allowance means. A name suricata would pass but the
// resolver refuses is a destination the user was told they had and does not; a
// name the resolver answers but suricata drops is a lookup that succeeds and a
// connection that hangs. One query, one generator per file, both in this binary.
//
// What the resolver adds that rules cannot is a verdict *before* the first packet
// leaves. A suricata rule cannot judge a tcp syn -- it carries no name -- so the
// handshake to an unknown address completes and only the request after it is
// dropped. A guest that cannot resolve the name never sends the syn at all, which
// is why the resolver, not the ruleset, is what makes "no egress by default" true
// for a VM that talks to the internet by name like everything else does.

// corednsRefuseAll is the fleet-wide fallback block: any client this file does
// not have a view for, and any name a client does have a view for but was not
// granted, is refused.
//
// REFUSED rather than NXDOMAIN. NXDOMAIN asserts that the name does not exist,
// which is a lie the guest's resolver will cache and which sends a confused user
// looking for a typo. REFUSED says the server would not answer, which is what
// happened, and stub resolvers do not cache it.
const corednsRefuseAll = `.:53 {
    bind ` + corednsBind + `
    template ANY ANY {
        rcode REFUSED
    }
` + corednsLogDirective + `    errors
}
`

// corednsBind is left for coredns to expand when it reads the file, because the
// address is a host fact this server does not have: the hello frame carries the
// VM pool and not the gateway, and adding it would be storing a second copy of
// something the host restates on every connect. dagent supplies the value on the
// container's command line.
//
// Binding at all is not about reach -- the input chain already admits 53 only
// from a tap and only to the gateway. It is that a host running systemd-resolved
// has a stub listener on 127.0.0.53:53, and on Linux a wildcard listener cannot
// share a port with a specific-address one, so a wildcarded coredns exits at
// startup on every ubuntu or debian host in the fleet.
const corednsBind = "{$DAGENT_GATEWAY}"

// corednsLogMarker prefixes every query line so the vector transform can tell
// them from coredns's own startup and plugin chatter, which goes to the same
// stream. A marker rather than a shape test: an INFO line about a failed upstream
// is not a query and must not be parsed as one.
const corednsLogMarker = "dagentdns"

// corednsLogDirective is the log plugin configured to emit one field-delimited
// line per query. The default format is meant to be read by a person and is a
// nuisance to parse; this one is four fields in a fixed order, which is what the
// remap in vector_config.go splits on.
//
// `class all` is explicit. The refusals are the rows worth having and they are not
// in the success class, so a future change to the plugin's default would take the
// denied panel down with it and nothing would say so.
const corednsLogDirective = `    log . "` + corednsLogMarker + ` {remote} {rcode} {type} {name}" {
        class all
    }
`

const corednsHeader = `# Written by the control server and installed by dagent. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
#
# Every guest on this host is pointed here by dhcp, and this file is the whole
# list of names any of them may resolve. A guest with no allowances gets no block
# of its own and falls through to the refuse-everything server at the bottom.
#
# Blocks are matched by zone first and then by view, and coredns consults the
# blocks that have a view before the ones that do not -- which is what makes the
# last block a fallback rather than a competitor.
`

// generateCoreDNSConfig compiles one host's Corefile. upstream is where an
// allowed lookup is forwarded, and rows are the same ones the ruleset is built
// from, in the same order, so the output is deterministic for a given database
// state.
func generateCoreDNSConfig(rows []db.ListVMNetworkTargetsByAgentRow, upstream string) string {
  var b strings.Builder
  b.WriteString(corednsHeader)

  for _, vm := range groupByVM(rows) {
    zones := vmZones(vm)
    if len(zones) == 0 {
      // Nothing to allow, so no block at all: the fallback refuses this guest
      // along with every client this file has never heard of. A block that
      // allowed nothing would say the same thing at more length.
      fmt.Fprintf(&b, "\n# %s (%s) at %s may resolve nothing\n",
        corefileText(vm.name), corefileText(vm.hostID), vm.ip)
      continue
    }

    fmt.Fprintf(&b, "\n# --- %s (%s) at %s ---\n",
      corefileText(vm.name), corefileText(vm.hostID), vm.ip)

    // One zone per allowed name. Zone matching is longest-suffix, so a block for
    // example.com also answers api.example.com -- the same "and any subdomain of
    // it" the dotprefix content match in local.rules means, which is the whole
    // reason the two files agree.
    fmt.Fprintf(&b, "%s:53 {\n", strings.Join(zones, ":53 "))
    fmt.Fprintf(&b, "    bind %s\n", corednsBind)
    fmt.Fprintf(&b, "    view %s {\n        expr client_ip() == '%s'\n    }\n", viewName(vm.ip, "allow"), vm.ip)
    fmt.Fprintf(&b, "    forward . %s\n", upstream)
    // Short, and only to collapse a retry storm from one guest into one upstream
    // query. A long cache here would outlive the allowance that permitted it.
    b.WriteString("    cache 30\n")
    b.WriteString(corednsLogDirective)
    b.WriteString("    errors\n}\n")

    // And the deny half, which is not optional. Without it this guest's lookups
    // for anything else fall through to the fleet-wide block below -- which
    // refuses them, so the verdict is the same, but a view-scoped block is what
    // keeps that verdict attributable to this VM in the query log.
    fmt.Fprintf(&b, ".:53 {\n    bind %s\n    view %s {\n        expr client_ip() == '%s'\n    }\n",
      corednsBind, viewName(vm.ip, "deny"), vm.ip)
    b.WriteString("    template ANY ANY {\n        rcode REFUSED\n    }\n")
    b.WriteString(corednsLogDirective)
    b.WriteString("    errors\n}\n")
  }

  b.WriteString("\n# --- everything else ------------------------------------------------------\n")
  b.WriteString("# Both the guests with no allowances and any client that is not a guest.\n")
  b.WriteString(corednsRefuseAll)
  return b.String()
}

// vmZones is the set of names one VM may resolve, deduplicated and sorted.
//
// Every domain allowance counts, including a lookup-only one -- that is the
// entire point of lookup-only, and it is how an allowance like ssh to a hostname
// is expressed: the name resolves here, and the access is granted by address in
// the ruleset.
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

// viewName builds a coredns view identifier from a VM address. Views share one
// namespace across the file, so the address is in the name: two VMs with the same
// view name would be one view, and the second would silently take the first's
// policy.
func viewName(ip, suffix string) string {
  return "vm_" + strings.NewReplacer(".", "_", ":", "_").Replace(ip) + "_" + suffix
}

// corefileText makes a free-text field safe to sit in a Corefile comment. Only
// the newline matters -- a comment runs to the end of the line, so a name
// carrying one would put whatever followed it into the file as configuration.
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

// pushCoreDNSConfig regenerates the host's Corefile and sends it. Called wherever
// pushSuricataRules is: the two files are compiled from the same rows and a host
// holding one of them at a newer version than the other is the disagreement this
// whole file exists to avoid.
//
// Failures are logged rather than returned, like the ruleset push: the allowance
// has already been recorded, and failing the request would tell the user their
// change did not happen when it did. An offline agent picks the file up when it
// reconnects.
// pushCoreDNSConfigToAll sends every connected agent a freshly compiled Corefile.
//
// One render per host rather than one broadcast, which is the difference between
// this and pushVectorConfigToAll: a Corefile is compiled from the VMs on a
// particular host, so a single envelope would carry one host's policy to all of
// them. Used when a setting every file embeds changes.
func pushCoreDNSConfigToAll(ctx context.Context, q *db.Queries, hub *Hub) {
  ids := hub.ConnectedIDs()
  sent := 0
  for _, id := range ids {
    agentID, err := parseUUID(id)
    if err != nil {
      log.Printf("connected agent %q does not have a usable id, so no corefile was sent", id)
      continue
    }
    pushCoreDNSConfig(ctx, q, hub, agentID)
    sent++
  }
  if sent > 0 {
    log.Printf("pushed a corefile to %d connected agent(s)", sent)
  }
}

func pushCoreDNSConfig(ctx context.Context, q *db.Queries, hub *Hub, agentID pgtype.UUID) {
  id := uuid.UUID(agentID.Bytes).String()
  rows, err := q.ListVMNetworkTargetsByAgent(ctx, agentID)
  if err != nil {
    log.Printf("could not read the allowed destinations for agent %s: %v", id, err)
    return
  }
  upstream := setting(ctx, q, settingResolverUpstream)
  if upstream == "" {
    // The default is not empty, so this is a setting somebody cleared. Sending a
    // Corefile with no forwarder would leave every guest on the host unable to
    // resolve anything; leaving the previous file in place is the less surprising
    // failure, and the log line is what says so.
    log.Printf("no resolver upstream is set, so no corefile was sent to agent %s", id)
    return
  }
  env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
    Kind: proto.KindCoreDNSConfig,
    File: &proto.FileConfig{Config: generateCoreDNSConfig(rows, upstream)},
  })
  if err != nil {
    log.Printf("could not build the corefile job for agent %s: %v", id, err)
    return
  }
  if err := hub.Send(id, env); err != nil {
    log.Printf("could not deliver the corefile to agent %s: %v", id, err)
  }
}
