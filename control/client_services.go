package main

import (
	"context"
	"errors"
	"log"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
	"control/internal/proto"
)

var releaseVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func validateReleaseVersion(v string) error {
	if !releaseVersionRe.MatchString(v) {
		return errors.New("must be a release number like 0.0.15")
	}
	return nil
}

func validateServiceDownloadURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return errors.New("must be a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must be an http or https URL")
	}
	if u.Host == "" {
		return errors.New("must have a host")
	}
	return nil
}

func serverReleaseVersion() string {
	if releaseVersionRe.MatchString(version) {
		return version
	}
	return ""
}

func serviceRelease(clientVer, clientURL, fleetURL string) proto.ServiceRelease {
	switch {
	case strings.TrimSpace(clientURL) != "":
		return proto.ServiceRelease{DownloadURL: strings.TrimSpace(clientURL)}
	case strings.TrimSpace(clientVer) != "":
		return proto.ServiceRelease{Version: strings.TrimSpace(clientVer)}
	case strings.TrimSpace(fleetURL) != "":
		return proto.ServiceRelease{DownloadURL: strings.TrimSpace(fleetURL)}
	default:
		return proto.ServiceRelease{Version: serverReleaseVersion()}
	}
}

func servicesConfigFor(ctx context.Context, q *db.Queries, c db.Client, force bool) proto.ServicesConfig {
	return proto.ServicesConfig{
		Dclient: serviceRelease(c.DclientVersion, c.DclientDownloadURL,
			setting(ctx, q, settingDclientDownloadURL)),
		Dpipe: serviceRelease(c.DpipeVersion, c.DpipeDownloadURL,
			setting(ctx, q, settingDpipeDownloadURL)),
		Proxy: serviceRelease(c.ProxyVersion, c.ProxyDownloadURL,
			setting(ctx, q, settingProxyDownloadURL)),
		Dinit: serviceRelease(c.DinitVersion, c.DinitDownloadURL,
			setting(ctx, q, settingDinitDownloadURL)),
		Force: force,
	}
}

func pushServicesConfig(ctx context.Context, q *db.Queries, hub *Hub, c db.Client, force bool) {
	id := uuid.UUID(c.ID.Bytes).String()
	cfg := servicesConfigFor(ctx, q, c, force)
	env, err := proto.NewEnvelope(proto.TypeJob, "", proto.Job{
		Kind:     proto.KindServicesConfig,
		Services: &cfg,
	})
	if err != nil {
		log.Printf("could not build the services job for client %s: %v", id, err)
		return
	}
	if err := hub.Send(id, env); err != nil {
		log.Printf("could not deliver the services config to client %s: %v", id, err)
	}
}

func recordInstalledVersions(ctx context.Context, q *db.Queries, id pgtype.UUID, clientID string, st *proto.ServicesState) {
	if st == nil {
		return
	}
	if err := q.UpdateClientInstalledVersions(ctx, db.UpdateClientInstalledVersionsParams{
		ID:                    id,
		DpipeInstalledVersion: st.Dpipe,
		ProxyInstalledVersion: st.Proxy,
		DinitInstalledVersion: st.Dinit,
	}); err != nil {
		log.Printf("client %s: could not record the installed versions: %v", clientID, err)
	}
}
