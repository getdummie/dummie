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
)

// Job is the payload of a TypeJob envelope. Exactly one of the per-kind fields
// is set, chosen by Kind.
type Job struct {
  Kind JobKind `json:"kind"`
  VM   *VMSpec `json:"vm,omitempty"` // set when Kind is KindVMCreate
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
