package main

import (
  "context"
  "log"
  "strings"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5/pgtype"

  "control/internal/proto"
)

// suricata.yaml is compiled here rather than seeded on the host, for two
// reasons that stack.
//
// The first is the one local.rules already had: what suricata enforces is the
// control plane's decision, and a file each host owned a copy of drifts the
// moment it is changed anywhere.
//
// The second only became true once events were shipped off the host. The
// eve-log types below decide which fields exist in the clickhouse rows this
// server queries -- no `dns` type means no dns__queries.rrname, which means the
// denied-domains panel is empty in a way that reads as "nothing was blocked".
// The vector transform and the migrations that back it already live here, and
// this is the third leg of the same tripod.
//
// The host keeps a minimal bootstrap copy of its own (see seedSuricataConfig in
// cmd/dagent/suricata.go): the container has to be able to start before this
// server has ever spoken to it, or a host that cannot reach the control plane
// drops all VM egress.

// suricataConfigTemplate is filled by generateSuricataConfig. HOME_NET is the
// only substitution, and it is a host fact -- reported in the agent's hello --
// so a rule written against $HOME_NET means "our guests" on every host
// regardless of what pool it was given.
const suricataConfigTemplate = `%YAML 1.1
---
# Written by the control server and installed by dagent. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
vars:
  address-groups:
    HOME_NET: "[__HOME_NET__]"

default-rule-path: /var/lib/suricata/rules
# local.rules is the whole ruleset. It is compiled by the control server from
# every vm on this host and pushed as one file, so there is no second file for
# a host-local signature set to live in.
rule-files:
  - local.rules

# The one record of what the ruleset actually did to a guest's traffic. Without
# it a drop is indistinguishable from a network fault from inside the VM, and
# from outside there is nothing at all to look at.
#
# http and tls are logged in their own right, not just as context on an alert.
# An alert only carries the hostname when the rule that fired matched a
# transaction -- a port-level drop fires at the SYN, before any ClientHello, so
# it can only ever name an address. These types answer "where was this guest
# going" for every handshake that got far enough to say so, allowed or denied.
#
# Every type here has columns behind it in migrations-clickhouse. Adding one
# without adding those columns means the events are shipped and silently
# dropped by the sink.
outputs:
  - eve-log:
      enabled: yes
      filetype: regular
      filename: eve.json
      types:
        - alert:
            # Attaches the app-layer record of the flow that alerted, which is
            # what puts tls.sni and http.hostname on a hostname-matched drop.
            metadata: yes
        - http:
            extended: yes
        - tls:
            extended: yes
        - dns
        - drop
        - anomaly

unix-command:
  enabled: yes
`

// generateSuricataConfig renders the file for one host. pool is the host's VM
// subnet, which only it knows.
func generateSuricataConfig(pool string) string {
  return strings.NewReplacer("__HOME_NET__", pool).Replace(suricataConfigTemplate)
}

// pushSuricataConfig sends one agent its suricata.yaml. Called on connect,
// which is the only time the pool is known -- it arrives in the hello frame and
// is not stored, because nothing else needs it and a column would be a second
// copy of a fact the host restates every time it connects.
//
// A host that reported no pool gets nothing rather than a config with an empty
// HOME_NET: that would judge every guest as external and quietly stop every
// $HOME_NET rule from matching, which is worse than leaving the bootstrap file
// in place.
func pushSuricataConfig(ctx context.Context, hub *Hub, agentID pgtype.UUID, pool string) {
  id := uuid.UUID(agentID.Bytes).String()
  if pool == "" {
    log.Printf("agent %s reported no vm pool, so no suricata config was sent", id)
    return
  }
  env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
    Kind: proto.KindSuricataConfig,
    File: &proto.FileConfig{Config: generateSuricataConfig(pool)},
  })
  if err != nil {
    log.Printf("could not build the suricata config job for agent %s: %v", id, err)
    return
  }
  if err := hub.Send(id, env); err != nil {
    log.Printf("could not deliver the suricata config to agent %s: %v", id, err)
  }
}
