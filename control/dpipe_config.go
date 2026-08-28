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
	dpipeCertDir  = "/etc/dpipe/certs"
	dpipeCertFile = dpipeCertDir + "/fullchain.pem"
	dpipeKeyFile  = dpipeCertDir + "/privkey.pem"

	dpipeNamedCertDir = dpipeCertDir + "/named"
)

const dpipeTLSTail = `  min_version: "1.2"
  dial_timeout: 5s
  resolve_timeout: 3s
  sniff_timeout: 5s
  sniff_max_bytes: 65536
`

func dpipeNamedCertPaths(sni string) (string, string) {
	dir := dpipeNamedCertDir + "/" + sni
	return dir + "/fullchain.pem", dir + "/privkey.pem"
}

func generateDpipeConfig(certs *proto.DpipeCerts) string {
	block := dpipeTLSDisabled
	if certs != nil && (certs.Cert != "" || len(certs.Named) > 0) {
		var b strings.Builder
		b.WriteString("tls:\n  enabled: true\n")
		if certs.Cert != "" && certs.Key != "" {
			b.WriteString("  # Written by dclient from the certificate this server issued for the domain.\n")
			fmt.Fprintf(&b, "  default_cert: %s\n", dpipeCertFile)
			fmt.Fprintf(&b, "  default_key: %s\n", dpipeKeyFile)
		}
		if len(certs.Named) > 0 {
			b.WriteString("  # One per custom domain, chosen by SNI.\n")
			b.WriteString("  certs:\n")
			for _, c := range certs.Named {
				cert, key := dpipeNamedCertPaths(c.SNI)
				fmt.Fprintf(&b, "    - sni: %s\n", yamlString(c.SNI))
				fmt.Fprintf(&b, "      cert: %s\n", yamlString(cert))
				fmt.Fprintf(&b, "      key: %s\n", yamlString(key))
			}
		}
		b.WriteString(dpipeTLSTail)
		block = b.String()
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
		File:       &proto.FileConfig{Config: generateDpipeConfig(certs)},
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
	out := &proto.DpipeCerts{}

	domain, err := q.GetClientDomain(ctx, clientID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	case domain.TlsEnabled && domain.CertObjectKey != "" && domain.KeyObjectKey != "":
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
		out.Cert, out.Key, out.Fingerprint = string(cert), string(key), domain.CertFingerprint
	}

	named, err := q.ListCustomDomainCertsByClient(ctx, clientID)
	if err != nil {
		return nil, fmt.Errorf("could not list the custom domain certificates: %w", err)
	}
	for _, n := range named {
		if blobs == nil {
			return nil, errNoBlobStore
		}
		cert, err := blobs.Get(ctx, n.CertObjectKey)
		if err != nil {
			log.Printf("skipping the certificate for %s: %v", n.Domain, err)
			continue
		}
		key, err := blobs.Get(ctx, n.KeyObjectKey)
		if err != nil {
			log.Printf("skipping the private key for %s: %v", n.Domain, err)
			continue
		}
		out.Named = append(out.Named, proto.DpipeNamedCert{
			SNI: n.Domain, Cert: string(cert), Key: string(key),
		})
	}

	if out.Cert == "" && len(out.Named) == 0 {
		return nil, nil
	}
	return out, nil
}
