package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
	"control/internal/proto"
)

const (
	// intproxyAddr is the last usable host in the vm pool, so a sequential
	// allocator never reaches it. dclient reserves it and puts it on a dummy
	// link; guests reach it through the default route they already have.
	intproxyAddr   = "10.64.255.254"
	intproxyListen = intproxyAddr + ":443"

	intproxyCertDir  = "/etc/intproxy/certs"
	intproxyCertFile = intproxyCertDir + "/fullchain.pem"
	intproxyKeyFile  = intproxyCertDir + "/privkey.pem"

	intproxyBrokerSocket = "/run/intproxy/broker.sock"
)

const intproxyConfigHeader = `# Written by the control server and installed by dclient. Edits on the host are
# overwritten: this file is replaced whenever the control server changes it.
#
# There is deliberately no policy here -- no map of which vm may reach which
# repository. intproxy asks the broker on every uncached request, so attaching
# or detaching an integration takes effect without a push or a restart, and a
# stale copy of the policy cannot exist.
`

const intproxyStaticConfig = `
# A concrete address, never a wildcard: dproxy holds 0.0.0.0:443 on this host and
# the two only coexist because this bind is more specific. freebind lets it
# succeed before dclient has created the address.
freebind: true
reuseport: true

# dclient serves this socket and relays to the control server with the host's own
# credential, so intproxy holds no credential of its own.
broker:
  socket: ` + intproxyBrokerSocket + `
  timeout: 5s
  token_skew: 60s

dial_timeout: 10s
tls_handshake_timeout: 10s

# A header deadline, not a body deadline. There is no read or write timeout at
# all: a packfile clone is one long request in each direction.
response_header_timeout: 60s

idle_timeout: 120s
read_header_timeout: 30s
shutdown_grace: 5m
log_level: info
`

func generateIntproxyConfig(tld, controlURL string) string {
	var b strings.Builder
	b.WriteString(intproxyConfigHeader)
	fmt.Fprintf(&b, "\nlisten: %s\n", yamlString(intproxyListen))
	fmt.Fprintf(&b, "tld: %s\n", yamlString(tld))
	fmt.Fprintf(&b, "label: %s\n", yamlString(proxyIntLabel))
	fmt.Fprintf(&b, "console_url: %s\n", yamlString(strings.TrimRight(strings.TrimSpace(controlURL), "/")))
	b.WriteString("\ntls:\n")
	fmt.Fprintf(&b, "  cert: %s\n", yamlString(intproxyCertFile))
	fmt.Fprintf(&b, "  key: %s\n", yamlString(intproxyKeyFile))
	b.WriteString("  min_version: \"1.2\"\n")
	b.WriteString(intproxyStaticConfig)
	return b.String()
}

// errCertLacksIntegrations means the fleet's certificate predates *.int.<tld>.
// Nothing is broken; integrations just stay off until it is reissued.
var errCertLacksIntegrations = errors.New("the certificate does not cover the integration names")

func pushIntproxyConfig(ctx context.Context, q *db.Queries, blobs *blobStore, hub *Hub, controlURL string, clientID pgtype.UUID) {
	id := uuid.UUID(clientID.Bytes).String()

	tld, cert, key, err := intproxyCertForClient(ctx, q, blobs, clientID)
	switch {
	case errors.Is(err, errCertLacksIntegrations):
		log.Printf("client %s has no certificate for *.%s.%s, so intproxy was not configured; reissue the fleet certificate to turn integrations on",
			id, proxyIntLabel, tld)
		return
	case err != nil:
		log.Printf("could not load the integration certificate for client %s: %v", id, err)
		return
	case tld == "":
		return
	}

	env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
		Kind: proto.KindIntproxyConfig,
		Intproxy: &proto.IntproxyConfig{
			Config: generateIntproxyConfig(tld, controlURL),
			Cert:   cert,
			Key:    key,
		},
	})
	if err != nil {
		log.Printf("could not build the intproxy config job for client %s: %v", id, err)
		return
	}
	if err := hub.Send(id, env); err != nil {
		log.Printf("could not deliver the intproxy config to client %s: %v", id, err)
	}
}

func intproxyCertForClient(ctx context.Context, q *db.Queries, blobs *blobStore, clientID pgtype.UUID) (string, string, string, error) {
	domain, err := q.GetClientDomain(ctx, clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", nil
	}
	if err != nil {
		return "", "", "", err
	}
	if !domain.TlsEnabled || domain.CertObjectKey == "" || domain.KeyObjectKey == "" {
		return domain.TLD, "", "", nil
	}
	if blobs == nil {
		return domain.TLD, "", "", errNoBlobStore
	}

	cert, err := blobs.Get(ctx, domain.CertObjectKey)
	if err != nil {
		return domain.TLD, "", "", fmt.Errorf("could not read the certificate for %s: %w", domain.TLD, err)
	}
	key, err := blobs.Get(ctx, domain.KeyObjectKey)
	if err != nil {
		return domain.TLD, "", "", fmt.Errorf("could not read the private key for %s", domain.TLD)
	}

	name := "*." + proxyIntLabel + "." + domain.TLD
	if _, err := validateCertificateFor(string(cert), string(key), name); err != nil {
		return domain.TLD, "", "", errCertLacksIntegrations
	}
	return domain.TLD, string(cert), string(key), nil
}
