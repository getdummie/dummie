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

// dpipe.yaml is compiled here for a narrower reason than dproxy.yaml. Almost
// nothing in it is a per-host fact: session limits, timeouts and log levels are
// policy about how the fleet behaves, and policy that lives in a write-once file
// on each host is policy nobody can change without visiting every machine.
//
// The one exception is tls, which depends on whether the domain this host
// belongs to has a certificate -- so the file is per-host after all, but only in
// that block.
//
// The key paths it names stay the host's. Those files are generated on the host
// by ensureDpipeKeys and never leave it, so this only says where to look.
//
// dclient keeps a bootstrap copy of its own for the window before the first
// push -- dpipe has to be able to start on a host that has never reached a
// control server.
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

// dpipeTLSDisabled is what a host with no certificate gets. Spelled rather than
// omitted for the same reason an empty proxy host table is: "this fleet is not
// on https" and "the tls block was never written" should not look alike in a
// file an operator reads to answer that question.
const dpipeTLSDisabled = `tls:
  enabled: false
`

// Where dclient writes the certificate that travels with this config. Named
// here as well as there because the two are different binaries: this file is
// what points dpipe at them, and dclient is what puts them down. The pair has to
// agree, exactly as proxyCookieSecretPath and its counterpart do.
const (
	dpipeCertFile = "/etc/dpipe/certs/fullchain.pem"
	dpipeKeyFile  = "/etc/dpipe/certs/privkey.pem"
)

// dpipeTLSEnabled points dpipe at the wildcard certificate dclient wrote.
//
// default_cert, not an entry in certs[]: dpipe selects from that list by exact
// SNI match, so a wildcard registered there would never be found -- "*.example
// .com" is not the name any client sends. The fallback is the right place for it
// anyway, because a client belongs to one domain and the wildcard cut for that
// domain is the certificate for every name the host serves.
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

// generateDpipeConfig compiles one host's dpipe.yaml. tls says whether this host
// has a certificate to serve; everything else is the same on every machine.
func generateDpipeConfig(tls bool) string {
	block := dpipeTLSDisabled
	if tls {
		block = dpipeTLSEnabled
	}
	return fmt.Sprintf(dpipeConfigTemplate, block)
}

// pushDpipeConfig sends one client its dpipe.yaml, and the certificate it names
// when the client's domain has one.
//
// Both in one job on purpose: the client applies each job in its own goroutine,
// so a config and a certificate sent separately have no order between them, and
// a file that turns tls on can arrive before the key material it points at.
func pushDpipeConfig(ctx context.Context, q *db.Queries, blobs *blobStore, hub *Hub, clientID pgtype.UUID) {
	id := uuid.UUID(clientID.Bytes).String()

	certs, err := dpipeCertsForClient(ctx, q, blobs, clientID)
	if err != nil {
		// Not fatal to the push. A host that gets the config without the certificate
		// keeps serving plain http, which is where it already was; sending nothing at
		// all would also withhold the ssh and console settings, which are unrelated
		// to any of this.
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

// dpipeCertsForClient reads the wildcard certificate for the domain a client
// belongs to. It returns nil, nil when there is nothing to send -- no domain, tls
// not turned on, or nothing issued yet -- which is the ordinary case and not an
// error.
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
		// The key's object key is deliberately not in the message: it is the one
		// string here that points straight at the private half.
		return nil, fmt.Errorf("could not read the private key for %s", domain.TLD)
	}

	return &proto.DpipeCerts{
		Cert:        string(cert),
		Key:         string(key),
		Fingerprint: domain.CertFingerprint,
	}, nil
}
