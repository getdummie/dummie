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
