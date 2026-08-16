package main

import (
  "context"
  "log"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5/pgtype"

  "control/internal/proto"
)

// dpipe.yaml is compiled here for a narrower reason than proxy.yaml. It holds
// no per-host fact at all: session limits, timeouts and log levels are policy
// about how the fleet behaves, and policy that lives in a write-once file on
// each host is policy nobody can change without visiting every machine.
//
// The key paths it names stay the host's. Those files are generated on the host
// by ensureDpipeKeys and never leave it, so this only says where to look.
//
// dagent keeps a bootstrap copy of its own for the window before the first
// push -- dpipe has to be able to start on a host that has never reached a
// control server.
const dpipeConfigTemplate = `# Written by the control server and installed by dagent. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
control_socket: /run/dpipe/control.sock
upgrade_socket: /run/dpipe/upgrade.sock
drain_timeout: 0s
log_level: info

# The key files are the host's own, generated there on first start and never
# sent anywhere. Only their location is decided here.
ssh:
  enabled: true
  host_key: /etc/dpipe/keys/dpipe_host_ed25519      # what the user's client pins
  client_key: /etc/dpipe/keys/dpipe_client_ed25519  # what the VM authorizes
  backend_known_hosts: ""
  dial_timeout: 5s
  resolve_timeout: 3s

tls:
  enabled: false

# The browser terminal. proxy decides who may open one; these are only the
# limits on what dpipe will run once it has been handed a session.
console:
  enabled: true
  idle_timeout: 30m
  max_sessions_per_host: 3
`

func generateDpipeConfig() string { return dpipeConfigTemplate }

// pushDpipeConfig sends one agent its dpipe.yaml. Carries no per-host fact, so
// every host gets the same bytes.
func pushDpipeConfig(ctx context.Context, hub *Hub, agentID pgtype.UUID) {
  id := uuid.UUID(agentID.Bytes).String()
  env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
    Kind: proto.KindDpipeConfig,
    File: &proto.FileConfig{Config: generateDpipeConfig()},
  })
  if err != nil {
    log.Printf("could not build the dpipe config job for agent %s: %v", id, err)
    return
  }
  if err := hub.Send(id, env); err != nil {
    log.Printf("could not deliver the dpipe config to agent %s: %v", id, err)
  }
}
