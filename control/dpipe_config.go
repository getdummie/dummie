package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
	"control/internal/proto"
)

const dpipeConfigTemplate = `# Written by the control server and installed by dclient. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
control_socket: /run/dpipe/control.sock
upgrade_socket: /run/dpipe/upgrade.sock

# 0 means a handed-over process waits forever for the sessions it is still
# carrying. Deliberate: the point of replacing dpipe by handover rather than by
# restart is that nobody's session dies for it, and a deadline would put that
# back for whoever happened to be connected.
#
# The cost is that such a process lives as long as its last session does, and a
# replacement while one is still draining leaves both in the cgroup. They are
# working processes holding real connections, not strays: systemctl status shows
# what each is still carrying.
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

%s
# The browser terminal. proxy decides who may open one; these are only the
# limits on what dpipe will run once it has been handed a session.
console:
  enabled: true
  idle_timeout: 30m
  max_sessions_per_host: 3
`

const dpipeTLSDisabled = `tls:
  enabled: false
`

const (
	dpipeCertFile = "/etc/dpipe/certs/fullchain.pem"
	dpipeKeyFile  = "/etc/dpipe/certs/privkey.pem"
)

var dpipeTLSEnabled = fmt.Sprintf(`tls:
  enabled: true
  # Written by dclient from the certificate this server issued for the domain.
  default_cert: %s
  default_key: %s
  min_version: "1.2"
  dial_timeout: 5s
  resolve_timeout: 3s
  sniff_timeout: 5s
  sniff_max_bytes: 65536
`, dpipeCertFile, dpipeKeyFile)

func generateDpipeConfig(tls bool) string {
	block := dpipeTLSDisabled
	if tls {
		block = dpipeTLSEnabled
	}
	return fmt.Sprintf(dpipeConfigTemplate, block)
}

func pushDpipeConfig(ctx context.Context, q *db.Queries, blobs *blobStore, hub *Hub, clientID pgtype.UUID) {
	id := uuid.UUID(clientID.Bytes).String()

	certs, err := dpipeCertsForClient(ctx, q, blobs, clientID)
	if err != nil {
		log.Printf("could not load the certificate for client %s, sending dpipe config without tls: %v", id, err)
		certs = nil
	}

	env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
		Kind:       proto.KindDpipeConfig,
		File:       &proto.FileConfig{Config: generateDpipeConfig(certs != nil)},
		DpipeCerts: certs,
	})
	if err != nil {
		log.Printf("could not build the dpipe config job for client %s: %v", id, err)
		return
	}
	if err := hub.Send(id, env); err != nil {
		log.Printf("could not deliver the dpipe config to client %s: %v", id, err)
	}
}

func dpipeCertsForClient(ctx context.Context, q *db.Queries, blobs *blobStore, clientID pgtype.UUID) (*proto.DpipeCerts, error) {
	domain, err := q.GetClientDomain(ctx, clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !domain.TlsEnabled || domain.CertObjectKey == "" || domain.KeyObjectKey == "" {
		return nil, nil
	}
	if blobs == nil {
		return nil, errNoBlobStore
	}

	cert, err := blobs.Get(ctx, domain.CertObjectKey)
	if err != nil {
		return nil, fmt.Errorf("could not read the certificate for %s: %w", domain.TLD, err)
	}
	key, err := blobs.Get(ctx, domain.KeyObjectKey)
	if err != nil {
		return nil, fmt.Errorf("could not read the private key for %s", domain.TLD)
	}

	return &proto.DpipeCerts{
		Cert:        string(cert),
		Key:         string(key),
		Fingerprint: domain.CertFingerprint,
	}, nil
}
