package main

import (
	"context"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/proto"
)

const suricataConfigTemplate = `%YAML 1.1
---
# Written by the control server and installed by dclient. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
vars:
  address-groups:
    HOME_NET: "[__HOME_NET__]"

# Only one direction of every flow reaches suricata. The forward chain accepts
# established/related by conntrack before the queue rule, so the vm-to-outside
# half is queued and the replies never are.
#
# That is deliberate -- it halves what suricata has to look at, and every
# verdict this policy makes is on a to-server packet anyway -- but it breaks the
# stream engine's default assumptions. Without the syn-ack the session is never
# established, so app-layer parsing never starts, so http and tls are never
# detected and the rules written against them cannot match. The symptom is
# silence: no events, no alerts, and every request on a web port passing.
#
# async-oneside is what tells the stream engine that a session it only ever sees
# one half of is normal rather than broken. Everything this policy matches on --
# the http host, the tls sni -- travels client-to-server, which is the half that
# is queued.
#
# The cost of only seeing one half is that a drop can never act on a response,
# which is why every verdict in the ruleset is written against a request. Guest
# dns is not in that list any more: it is answered by the resolver on the gateway
# and never reaches the queue at all. The dns parser and eve type below stay for
# the guest that ignores the resolver and aims a query somewhere else, which is
# denied and logged with the name it asked for.
stream:
  async-oneside: yes

# Suricata reads this file instead of the one in the image -- a mounted
# suricata.yaml replaces the image's, it does not merge with it -- so anything
# left out here is left to whatever the binary was compiled with, and for the
# app-layer parsers that is not enough.
#
# Without a libhtp config the http parser never initialises. It fails quietly:
# detection still runs, http rules simply never match, and every request on port
# 80 passes. Nothing in the logs says so, because a passed packet writes no
# event and the parser that would have written one is the thing that is missing.
#
# The hostname half of this policy -- the http and tls denies, and every pass
# rule written against a domain -- is only enforced because of this block.
#
# A parser not named below is not disabled: it falls back to whatever the binary
# was compiled with, which for almost all of them is enabled. The ruleset depends
# on that. Its deny on the web ports is a positive allowlist -- drop unless this
# is http or tls -- and what makes that catch ssh on 443 without also catching
# every legitimate request is suricata being able to positively identify ssh.
# Turning the other parsers off here would not make the rule safer; it would make
# it blunt, and take every non-web protocol's identity out of the logs with it.
app-layer:
  protocols:
    http:
      enabled: yes
      libhtp:
        default-config:
          personality: IDS
          # Bodies are never inspected: every rule here matches on the host, the
          # sni or the query name. These are the buffering ceiling, not a
          # feature -- 0 would mean unlimited.
          request-body-limit: 16kb
          response-body-limit: 16kb
    tls:
      enabled: yes
      detection-ports:
        dp: 443
    dns:
      tcp:
        enabled: yes
        detection-ports:
          dp: 53
      udp:
        enabled: yes
        detection-ports:
          dp: 53

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

func generateSuricataConfig(pool string) string {
	return strings.NewReplacer("__HOME_NET__", pool).Replace(suricataConfigTemplate)
}

func pushSuricataConfig(ctx context.Context, hub *Hub, clientID pgtype.UUID, pool string) {
	id := uuid.UUID(clientID.Bytes).String()
	if pool == "" {
		log.Printf("client %s reported no vm pool, so no suricata config was sent", id)
		return
	}
	env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
		Kind: proto.KindSuricataConfig,
		File: &proto.FileConfig{Config: generateSuricataConfig(pool)},
	})
	if err != nil {
		log.Printf("could not build the suricata config job for client %s: %v", id, err)
		return
	}
	if err := hub.Send(id, env); err != nil {
		log.Printf("could not deliver the suricata config to client %s: %v", id, err)
	}
}
