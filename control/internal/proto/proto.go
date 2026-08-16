// Package proto holds the wire types shared by the control server and the
// dagent binary. It has no dependencies beyond the standard library so the
// agent build stays small.
package proto

import (
  "encoding/json"
  "time"
)

// ConnectPath is the WebSocket endpoint agents dial; EnrollPath is the one-shot
// HTTP enrollment endpoint. Both are defined here so server and agent cannot
// drift apart.
const (
  EnrollPath  = "/api/v1/agent/enroll"
  ConnectPath = "/api/v1/agent/connect"
)

// MsgType discriminates the Envelope. Adding a new kind of pushed work means
// adding a constant and a payload struct -- the frame itself does not change.
type MsgType string

const (
  TypeHello    MsgType = "hello"     // agent -> server, always the first frame
  TypeHelloAck MsgType = "hello_ack" // server -> agent
  TypeJob      MsgType = "job"       // server -> agent
  TypeResult   MsgType = "result"    // agent -> server
  TypeMetrics   MsgType = "metrics"   // agent -> server, unsolicited and periodic
  TypeInventory MsgType = "inventory" // agent -> server, unsolicited and periodic
  TypeError    MsgType = "error"     // either direction
)

// Envelope is the only thing ever written to the socket.
type Envelope struct {
  Type    MsgType         `json:"type"`
  ID      string          `json:"id,omitempty"` // correlates a result with its job
  Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope marshals payload into an Envelope. A nil payload is allowed.
func NewEnvelope(t MsgType, id string, payload any) (Envelope, error) {
  env := Envelope{Type: t, ID: id}
  if payload == nil {
    return env, nil
  }
  b, err := json.Marshal(payload)
  if err != nil {
    return Envelope{}, err
  }
  env.Payload = b
  return env, nil
}

// Hello is the agent's opening frame. The server trusts it only for
// descriptive facts -- identity comes from the bearer token on the upgrade.
type Hello struct {
  Version   string `json:"version"`
  MachineID string `json:"machine_id"`
  Hostname  string `json:"hostname"`
  OS        string `json:"os"`
  OSVersion string `json:"os_version"`
  Arch      string `json:"arch"`

  // Pool is the host's VM subnet in CIDR form. Reported because the suricata
  // config the server compiles needs it for HOME_NET, and the pool is the one
  // thing in that file the control plane cannot know: it is set per host, and
  // guessing it would make every guest external on a host that changed it.
  //
  // Empty from an agent with no network config, or an older one. The server
  // treats that as "cannot compile a config for this host" rather than
  // substituting a default.
  Pool string `json:"pool,omitempty"`
}

type HelloAck struct {
  AgentID    string    `json:"agent_id"`
  ServerTime time.Time `json:"server_time"`
}

type ErrorPayload struct {
  Message string `json:"message"`
}

// --- jobs -------------------------------------------------------------------

// JobKind says what a job frame asks for. The kind is inside the payload rather
// than in MsgType so the envelope keeps one frame type for all pushed work and
// the correlation id keeps its single meaning.
type JobKind string

const (
  KindVMCreate JobKind = "vm.create"
  // KindVMStop shuts the guest down but leaves everything it owns on the host,
  // so KindVMStart can boot it again. KindVMDestroy is the one that cannot be
  // undone.
  KindVMStop    JobKind = "vm.stop"
  KindVMStart   JobKind = "vm.start"
  KindVMDestroy JobKind = "vm.destroy"

  // KindSuricataRules replaces the host's whole local.rules file. It names no
  // VM because the file is the host's, not one guest's: the pass rules for
  // every VM and the default deny they sit above have to be written together or
  // the ones left out stop being enforced.
  KindSuricataRules JobKind = "suricata.rules"

  // KindProxyConfig replaces the host's whole proxy.yaml, for the same reason:
  // proxy routes an inbound ssh session by which key authenticated it, so the
  // entry for every VM on the host is one list that has to be written together.
  // A file carrying only the VM that just changed would revoke the rest.
  KindProxyConfig JobKind = "proxy.config"

  // KindSuricataConfig replaces the host's whole suricata.yaml. It is the
  // control plane's file for the same reason local.rules is -- and for one more
  // that only became true once events were shipped off the host: the eve-log
  // types it enables decide which fields exist in the clickhouse rows the
  // control server queries. Left on the host, adding a column to that schema
  // would mean editing a file on every machine before anything could fill it.
  KindSuricataConfig JobKind = "suricata.config"

  // KindDpipeConfig replaces the host's whole dpipe.yaml. Session limits and
  // timeouts are fleet policy rather than facts about a machine, so they belong
  // where they can be changed once. The key paths it names stay the host's --
  // the keys are generated there and never leave.
  KindDpipeConfig JobKind = "dpipe.config"

  // KindVectorConfig replaces the host's whole vector.yaml and names the vector
  // release to run it. Compiled by the control server for the same reason the
  // two above are: the transform in it writes exactly the columns this repo's
  // clickhouse migrations create, so the file and the schema have to ship in
  // one deployable. Rendered on the host, a schema change would mean rolling a
  // new agent to every machine in the fleet before the columns could be used.
  KindVectorConfig JobKind = "vector.config"
)

// Job is the payload of a TypeJob envelope. Which fields are set is chosen by
// Kind: a create carries a spec, everything else names an existing VM.
type Job struct {
  Kind JobKind `json:"kind"`
  VM   *VMSpec `json:"vm,omitempty"` // set when Kind is KindVMCreate
  // VMID is the agent's own short id, as reported in VMInfo or an inventory.
  // Set for every kind that acts on a VM that already exists.
  VMID string `json:"vm_id,omitempty"`
  // Suricata is set when Kind is KindSuricataRules.
  Suricata *SuricataRules `json:"suricata,omitempty"`
  // Proxy is set when Kind is KindProxyConfig.
  Proxy *ProxyConfig `json:"proxy,omitempty"`
  // Vector is set when Kind is KindVectorConfig.
  Vector *VectorConfig `json:"vector,omitempty"`
  // File is set when Kind is KindSuricataConfig or KindDpipeConfig. Both carry
  // nothing but a whole file, so they share one payload rather than each having
  // a struct with a single Config field in it.
  File *FileConfig `json:"file,omitempty"`
}

// FileConfig is a complete config file for a service on the host. The agent
// writes it as given and never merges, then restarts whatever reads it -- only
// if the content actually changed, because both services it is used for are
// disruptive to restart.
type FileConfig struct {
  Config string `json:"config"`
}

// VectorConfig is a complete vector.yaml plus the release meant to run it. The
// agent writes the file as given and never merges: the transform in it is the
// control plane's statement about how eve.json maps onto its own clickhouse
// schema, and a host holding an opinion about that would be a second answer
// nobody can audit from the control plane.
type VectorConfig struct {
  // Version is a bare release number, e.g. "0.57.0". The agent builds the
  // download URL from it, so the server validates the shape before storing it:
  // this ends up in a URL whose contents are installed and run as root. The
  // agent checks it again rather than trusting that.
  Version string `json:"version"`

  // Config is the whole file. Empty means the fleet has no clickhouse endpoint
  // configured, and the agent should install nothing rather than write a config
  // pointing nowhere.
  //
  // It contains the clickhouse password, so it must not be logged or echoed
  // back in a result frame, and the file it lands in is the agent's to create
  // 0600.
  Config string `json:"config,omitempty"`
}

// SuricataRules carries a complete local.rules. The control server compiles it
// from every VM on the host, so the agent never merges or edits -- it writes the
// file as given and tells Suricata to reload. Anything cleverer on the host
// would be a second opinion about policy in the one place that cannot be
// audited from the control plane.
type SuricataRules struct {
  Rules string `json:"rules"`
}

// ProxyConfig carries a complete proxy.yaml. Like SuricataRules the agent writes
// it as given and never merges: the ssh user list is derived from which user owns
// which VM, and that is knowable only in the control plane.
type ProxyConfig struct {
  Config string `json:"config"`
  // CookieSecret is the key proxy verifies login tokens with, and the same
  // bytes the control server signs them with. It travels with the config
  // because the config names the file it belongs in: provisioned separately,
  // a host could end up with an auth block pointing at a key it does not have.
  //
  // The agent writes it to CookieSecretPath verbatim -- no trailing newline,
  // no re-encoding -- and the file is the agent's to create with 0600 and a
  // 0700 parent. It must never be logged or echoed back in a result frame.
  //
  // Empty when the control server has no key configured, which is also when
  // the config carries no auth block; the agent should leave any existing file
  // alone in that case rather than truncating it.
  CookieSecret     string `json:"cookie_secret,omitempty"`
  CookieSecretPath string `json:"cookie_secret_path,omitempty"`
}

// VMSpec is one VM creation request. It is the same shape the agent's own unix
// socket accepts, so a VM asked for by the control plane and one asked for by
// `dagent vm create` are the same request travelling by different routes.
type VMSpec struct {
  Name string `json:"name,omitempty"`
  Boot string `json:"boot,omitempty"`

  Kernel       string `json:"kernel,omitempty"`
  KernelSHA    string `json:"kernel_sha256,omitempty"`
  Initrd       string `json:"initrd,omitempty"`
  InitrdSHA    string `json:"initrd_sha256,omitempty"`
  Rootfs       string `json:"rootfs,omitempty"`
  RootfsSHA    string `json:"rootfs_sha256,omitempty"`
  RootfsTar    string `json:"rootfs_tar,omitempty"`
  RootfsTarSHA string `json:"rootfs_tar_sha256,omitempty"`
  RootfsSize   string `json:"rootfs_size,omitempty"`
  Disk         string `json:"disk,omitempty"`
  DiskSHA      string `json:"disk_sha256,omitempty"`
  DiskSize     string `json:"disk_size,omitempty"`
  Append       string `json:"append,omitempty"`
  Firmware     string `json:"firmware,omitempty"`

  CPUs   int `json:"cpus,omitempty"`
  Memory int `json:"memory_mib,omitempty"`

  NoNetwork bool     `json:"no_network,omitempty"`
  IP        string   `json:"ip,omitempty"`
  Egress    []string `json:"egress,omitempty"`
  EgressAny bool     `json:"egress_any,omitempty"`
  RateMbit  int      `json:"rate_mbit,omitempty"`
  BurstKbit int      `json:"burst_kbit,omitempty"`
}

// JobResult is the payload of a TypeResult envelope, correlated to its job by
// the envelope id. A failure is a result too -- the server needs to hear about
// it, and an error frame carries no correlation.
type JobResult struct {
  Kind  JobKind `json:"kind"`
  OK    bool    `json:"ok"`
  Error string  `json:"error,omitempty"`
  VM    *VMInfo `json:"vm,omitempty"` // set when Kind is KindVMCreate and OK
}

// VMInfo is what the agent actually built. The id and the address are allocated
// on the host, so this is the only place the control plane learns them.
type VMInfo struct {
  ID        string `json:"id"`
  Name      string `json:"name"`
  Boot      string `json:"boot"`
  CPUs      int    `json:"cpus"`
  MemoryMiB int    `json:"memory_mib"`
  IP        string `json:"ip,omitempty"`
}

// --- inventory ---------------------------------------------------------------

// Inventory is the full set of VMs on the host, reported periodically. It is
// deliberately a complete list rather than a diff: the agent's own directory is
// the truth about what exists, and a snapshot means a missed frame self-corrects
// on the next tick instead of leaving the server permanently out of step.
//
// It is also how a VM created locally with `dagent vm create` becomes visible to
// the control plane at all.
type Inventory struct {
  VMs []VMState `json:"vms"`
}

// VMState is one VM as the host currently sees it. Running is live state -- is
// there a qemu process -- while everything in VMInfo is what the VM was made as.
type VMState struct {
  VMInfo
  Running   bool      `json:"running"`
  CreatedAt time.Time `json:"created_at"`
}

// --- metrics ----------------------------------------------------------------

// Metrics is a snapshot of the host the agent runs on, pushed periodically.
// Disk is the filesystem holding the agent's data directory -- the one that
// fills up when images and overlays accumulate, which is the one that matters.
type Metrics struct {
  CPUCount       int     `json:"cpu_count"`
  CPUPercent     float64 `json:"cpu_percent"`
  Load1          float64 `json:"load1"`
  Load5          float64 `json:"load5"`
  Load15         float64 `json:"load15"`
  MemTotalBytes  int64   `json:"mem_total_bytes"`
  MemUsedBytes   int64   `json:"mem_used_bytes"`
  DiskTotalBytes int64   `json:"disk_total_bytes"`
  DiskUsedBytes  int64   `json:"disk_used_bytes"`
  UptimeSeconds  int64   `json:"uptime_seconds"`
}

// EnrollRequest is the body of POST /api/v1/agent/enroll.
type EnrollRequest struct {
  Key       string `json:"key"`
  MachineID string `json:"machine_id"`
  Hostname  string `json:"hostname"`
  OS        string `json:"os"`
  OSVersion string `json:"os_version"`
  Arch      string `json:"arch"`
  Version   string `json:"version"`
}

// EnrollResponse carries the per-agent token. It is returned exactly once and
// is never recoverable afterwards -- the server stores only its hash.
type EnrollResponse struct {
  AgentID string `json:"agent_id"`
  Token   string `json:"token"`
}
