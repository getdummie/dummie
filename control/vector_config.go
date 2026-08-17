package main

import (
  "context"
  "log"
  "strings"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5/pgtype"

  "control/internal/db"
  "control/internal/proto"
)

// vector.yaml is compiled here rather than on the host, and the reason is the
// transform below: it writes exactly the columns migrations-clickhouse creates.
// The two have to move together, and they only do if they ship in the same
// binary. Rendered on the host, adding a column would mean rolling a new agent
// to every machine in the fleet before anything could write to it.
//
// So a schema change is: write the migration, update the transform beside it,
// `migrate-clickhouse up`, deploy. Agents are sent the new file on their next
// connect, or immediately if they are online.
//
// Order matters in one direction only. Adding columns is safe either way round
// -- and note that the sink's skip_unknown_fields means a transform writing a
// column that does not exist yet is silently dropped rather than failing the
// batch, which is forgiving and quiet in equal measure. Renames and drops need
// expand/contract: add, write both, then drop.

// eveLogPath is where dagent's suricata writes its events. Named here because
// this file is the source of the config, but it is dagent's decision -- see
// suricataLogDir in cmd/dagent/suricata.go.
const eveLogPath = "/var/log/suricata/eve.json"

// The destination is fixed rather than a setting: the transform writes exactly
// what migrations-clickhouse creates, so pointing it at another table could
// only ever produce rejected inserts.
const (
  vectorClickHouseDatabase = "dummie"
  vectorClickHouseTable    = "suricata_events"
  vectorDNSTable           = "dns_queries"
)

// corednsLogContainer is the container vector reads the resolver's query log from.
// Named here because this file is the source of the config, but it is dagent's
// decision -- see corednsContainer in cmd/dagent/coredns.go.
const corednsLogContainer = "coredns"

// vectorConfigTemplate is filled by renderVectorConfig. Placeholders rather
// than fmt verbs because the VRL is dense with %-formatted timestamps, and
// every one of them would have to be escaped.
const vectorConfigTemplate = `# Written by dagent from the control server's settings. Edits are overwritten:
# this file is replaced whenever those settings or the control server change.
sources:
  eve:
    type: file
    include: ["__EVE_LOG__"]

  # The resolver's query log. Read from docker rather than a file because the
  # coredns log plugin only ever writes to standard output -- there is no file for
  # a file source to follow. vector runs as root, so the socket is readable.
  coredns:
    type: docker_logs
    include_containers: ["__COREDNS_CONTAINER__"]

transforms:
  # coredns writes its startup and plugin chatter to the same stream as its
  # queries, and an INFO line about an upstream timing out is not a query. The
  # marker is what tells them apart; shape alone would not.
  dns_queries_only:
    type: filter
    inputs: ["coredns"]
    condition: 'contains(string!(.message), "__DNS_MARKER__ ")'

  shape_dns:
    type: remap
    inputs: ["dns_queries_only"]
    source: |
      raw = string!(.message)
      # Everything after the marker: the four fields the log directive in
      # coredns_config.go emits, in that order. Indexing cannot fail in vrl, it
      # yields null -- so the coalescing goes on the string() coercion, which can,
      # exactly as the eve transform below does it.
      parts = split(raw, "__DNS_MARKER__ ")
      f = split(strip_whitespace(string(parts[1]) ?? ""), " ")

      # docker hands the line over with a timestamp already parsed, unlike eve.json
      # where it is a field in the payload.
      ts = timestamp(.timestamp) ?? now()
      . = {}
      .timestamp = format_timestamp!(ts, "%Y-%m-%d %H:%M:%S%.6f")
      .src_ip = string(f[0]) ?? "::"
      .rcode = string(f[1]) ?? ""
      .qtype = string(f[2]) ?? ""
      # dns names arrive fully qualified and in whatever case the guest asked in.
      # Stored lowercased and without the root dot so a row compares directly
      # against a recorded destination and against suricata_events.domain.
      .qname = replace(downcase(string(f[3]) ?? ""), r'\.$', "")

  shape:
    type: remap
    inputs: ["eve"]
    source: |
      raw = string!(.message)
      ev = object!(parse_json!(raw))
      ts = parse_timestamp!(ev.timestamp, "%Y-%m-%dT%H:%M:%S%.f%z")

      . = {}
      .timestamp = format_timestamp!(ts, "%Y-%m-%d %H:%M:%S%.6f")
      .event_type = string(ev.event_type) ?? ""
      .flow_id = to_int(ev.flow_id) ?? 0
      .pkt_src = string(ev.pkt_src) ?? ""
      .direction = string(ev.direction) ?? ""
      .src_ip = string(ev.src_ip) ?? "::"
      .dest_ip = string(ev.dest_ip) ?? "::"
      .src_port = to_int(ev.src_port) ?? 0
      .dest_port = to_int(ev.dest_port) ?? 0
      .proto = string(ev.proto) ?? ""
      .ip_v = to_int(ev.ip_v) ?? 0
      .app_proto = string(ev.app_proto) ?? ""

      .alert__action = string(ev.alert.action) ?? ""
      .alert__gid = to_int(ev.alert.gid) ?? 0
      .alert__signature_id = to_int(ev.alert.signature_id) ?? 0
      .alert__rev = to_int(ev.alert.rev) ?? 0
      .alert__signature = string(ev.alert.signature) ?? ""
      .alert__category = string(ev.alert.category) ?? ""
      .alert__severity = to_int(ev.alert.severity) ?? 0

      .flow__pkts_toserver = to_int(ev.flow.pkts_toserver) ?? 0
      .flow__pkts_toclient = to_int(ev.flow.pkts_toclient) ?? 0
      .flow__bytes_toserver = to_int(ev.flow.bytes_toserver) ?? 0
      .flow__bytes_toclient = to_int(ev.flow.bytes_toclient) ?? 0
      .flow__start = null
      if exists(ev.flow.start) {
        .flow__start = format_timestamp!(
          parse_timestamp!(ev.flow.start, "%Y-%m-%dT%H:%M:%S%.f%z"),
          "%Y-%m-%d %H:%M:%S%.6f")
      }

      .drop__reason = string(ev.drop.reason) ?? ""
      .drop__len = to_int(ev.drop.len) ?? 0
      .drop__ttl = to_int(ev.drop.ttl) ?? 0
      .drop__tos = to_int(ev.drop.tos) ?? 0
      .drop__ipid = to_int(ev.drop.ipid) ?? 0
      .drop__udplen = to_int(ev.drop.udplen) ?? 0
      .drop__tcpseq = to_int(ev.drop.tcpseq) ?? 0
      .drop__tcpack = to_int(ev.drop.tcpack) ?? 0
      .drop__tcpwin = to_int(ev.drop.tcpwin) ?? 0
      .drop__tcpres = to_int(ev.drop.tcpres) ?? 0
      .drop__tcpurgp = to_int(ev.drop.tcpurgp) ?? 0
      .drop__syn = bool(ev.drop.syn) ?? false
      .drop__ack = bool(ev.drop.ack) ?? false
      .drop__psh = bool(ev.drop.psh) ?? false
      .drop__rst = bool(ev.drop.rst) ?? false
      .drop__urg = bool(ev.drop.urg) ?? false
      .drop__fin = bool(ev.drop.fin) ?? false

      .dns__version = to_int(ev.dns.version) ?? 0
      .dns__type = string(ev.dns.type) ?? ""
      .dns__id = to_int(ev.dns.id) ?? 0
      .dns__tx_id = to_int(ev.dns.tx_id) ?? 0
      .dns__flags = string(ev.dns.flags) ?? ""
      .dns__rd = bool(ev.dns.rd) ?? false
      .dns__opcode = to_int(ev.dns.opcode) ?? 0
      .dns__rcode = string(ev.dns.rcode) ?? ""
      queries = array(ev.dns.queries) ?? []
      ."dns__queries.rrname" = map_values(queries) -> |q| { string(q.rrname) ?? "" }
      ."dns__queries.rrtype" = map_values(queries) -> |q| { string(q.rrtype) ?? "" }

      .tls__sni = string(ev.tls.sni) ?? ""
      .tls__version = string(ev.tls.version) ?? ""
      .http__hostname = string(ev.http.hostname) ?? ""
      .http__url = string(ev.http.url) ?? ""
      .http__method = string(ev.http.http_method) ?? ""
      .http__status = to_int(ev.http.status) ?? 0

      .anomaly__type = string(ev.anomaly.type) ?? ""
      .anomaly__event = string(ev.anomaly.event) ?? ""
      .anomaly__layer = string(ev.anomaly.layer) ?? ""

sinks:
  clickhouse:
    type: clickhouse
    inputs: ["shape"]
    endpoint: __CLICKHOUSE_URL__
    database: __DATABASE__
    table: __TABLE__
    auth:
      strategy: basic
      user: __CLICKHOUSE_USER__
      password: __CLICKHOUSE_PASSWORD__
    skip_unknown_fields: true
    batch:
      timeout_secs: 5
    buffer:
      type: disk
      max_size: 268435488

  # A second sink rather than a second table in the first: the two carry different
  # columns, and the clickhouse sink writes one table.
  clickhouse_dns:
    type: clickhouse
    inputs: ["shape_dns"]
    endpoint: __CLICKHOUSE_URL__
    database: __DATABASE__
    table: __DNS_TABLE__
    auth:
      strategy: basic
      user: __CLICKHOUSE_USER__
      password: __CLICKHOUSE_PASSWORD__
    skip_unknown_fields: true
    batch:
      timeout_secs: 5
    buffer:
      type: disk
      max_size: 268435488
`

// renderVectorConfig produces the file for one set of settings. Every
// substituted value is yaml-quoted rather than interpolated raw: a password is
// whatever an operator typed, and one containing a colon or a leading brace
// would otherwise turn the sink block into something else entirely.
func renderVectorConfig(url, user, password string) string {
  return strings.NewReplacer(
    // Both of these are constants in this repo rather than operator input, so
    // they are substituted raw into quotes the template already has -- the same
    // way eveLogPath's include: line does it. yamlString is for the settings
    // below, whose contents are whatever somebody typed.
    "__EVE_LOG__", eveLogPath,
    "__COREDNS_CONTAINER__", corednsLogContainer,
    "__DNS_MARKER__", corednsLogMarker,
    "__CLICKHOUSE_URL__", yamlString(url),
    "__CLICKHOUSE_USER__", yamlString(user),
    "__CLICKHOUSE_PASSWORD__", yamlString(password),
    "__DATABASE__", yamlString(vectorClickHouseDatabase),
    "__TABLE__", yamlString(vectorClickHouseTable),
    "__DNS_TABLE__", yamlString(vectorDNSTable),
  ).Replace(vectorConfigTemplate)
}

func isVectorSetting(key string) bool {
  switch key {
  case settingVectorVersion, settingClickHouseURL, settingClickHouseUser, settingClickHousePassword:
    return true
  }
  return false
}

// vectorConfigFromSettings builds the job payload. An unset clickhouse url
// yields an empty Config, which is how an operator turns event shipping off:
// the agent installs nothing rather than writing a config pointing nowhere.
func vectorConfigFromSettings(ctx context.Context, q *db.Queries) proto.VectorConfig {
  cfg := proto.VectorConfig{Version: setting(ctx, q, settingVectorVersion)}
  url := setting(ctx, q, settingClickHouseURL)
  if url == "" {
    return cfg
  }
  cfg.Config = renderVectorConfig(url,
    setting(ctx, q, settingClickHouseUser),
    setting(ctx, q, settingClickHousePassword))
  return cfg
}

// pushVectorConfig sends one agent its vector.yaml. Called on every connect and
// whenever an admin changes one of the settings.
//
// Failures are logged rather than returned: nothing the caller is doing depends
// on the host having caught up, and an offline agent is the normal case rather
// than an error -- it is sent one as soon as it reconnects.
func pushVectorConfig(ctx context.Context, q *db.Queries, hub *Hub, agentID pgtype.UUID) {
  id := uuid.UUID(agentID.Bytes).String()
  env, err := buildVectorConfigEnvelope(ctx, q)
  if err != nil {
    log.Printf("could not build the vector config job for agent %s: %v", id, err)
    return
  }
  if err := hub.Send(id, env); err != nil {
    log.Printf("could not deliver the vector config to agent %s: %v", id, err)
  }
}

// pushVectorConfigToAll sends every connected agent the current config. It
// carries no per-host fact, so this is one envelope broadcast rather than a
// render per agent.
func pushVectorConfigToAll(ctx context.Context, q *db.Queries, hub *Hub) {
  env, err := buildVectorConfigEnvelope(ctx, q)
  if err != nil {
    log.Printf("could not build the vector config job: %v", err)
    return
  }
  if n := hub.Broadcast(env); n > 0 {
    log.Printf("pushed the vector config to %d connected agent(s)", n)
  }
}

func buildVectorConfigEnvelope(ctx context.Context, q *db.Queries) (proto.Envelope, error) {
  cfg := vectorConfigFromSettings(ctx, q)
  return proto.NewEnvelope(proto.TypeJob, "", proto.Job{
    Kind:   proto.KindVectorConfig,
    Vector: &cfg,
  })
}
