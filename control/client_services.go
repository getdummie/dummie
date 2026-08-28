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

// Which build of dclient, dpipe and dproxy each host runs is the control plane's
// call, not the host's. It used to be the other way round -- every host named its
// own download URLs in /etc/dclient/config.yaml -- which made upgrading the fleet
// an ssh loop, and left nothing here able to say what any machine was actually
// running.
//
// Per client rather than a fleet-wide setting, unlike vector: these three are the
// data path, so moving them is exactly the thing an operator wants to do one host
// at a time and watch before doing it to the rest.

// releaseVersionRe is deliberately strict: the value is interpolated into a
// github release URL whose contents are installed and executed as root on every
// host it reaches. Shared with the fleet-wide vector version, which is the same
// kind of value checked for the same reason.
var releaseVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func validateReleaseVersion(v string) error {
	if !releaseVersionRe.MatchString(v) {
		return errors.New("must be a release number like 0.0.15")
	}
	return nil
}

// validateServiceDownloadURL accepts an empty value -- that is how an operator
// goes back to the version -- but anything else has to be a URL a host can fetch.
// Plain http is allowed because the artifact server is often on the fleet's own
// network, and the client warns about it there.
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

// serverReleaseVersion is what a host runs when nobody has said otherwise: this
// server's own version, so a fleet that never touches these fields tracks the
// control plane it talks to.
//
// Empty from a development build, whose version is "dev". The client is sent the
// empty string rather than a guess, and installs nothing until an operator names
// a release or a URL.
func serverReleaseVersion() string {
	if releaseVersionRe.MatchString(version) {
		return version
	}
	return ""
}

// serviceRelease decides where one binary comes from, most specific first:
//
//  1. the URL on the client's own row -- one machine, one build, usually because
//     someone is testing something on it
//  2. the version on the client's own row -- one machine moved onto a release
//  3. the fleet's default URL, from the settings -- an installation that builds
//     its own binaries and serves them from somewhere the control plane does not
//     know how to name
//  4. this control server's own version, as a published release -- which is what
//     a normal installation runs and never configures
//
// Exactly one of the two fields comes back set, so there is no second precedence
// rule hiding on the client side. The version is passed through rather than turned
// into a URL here because the client is what knows the release layout, and having
// two places that know it is how they come to disagree.
func serviceRelease(clientVer, clientURL, fleetURL string) proto.ServiceRelease {
	switch {
	case strings.TrimSpace(clientURL) != "":
		return proto.ServiceRelease{DownloadURL: strings.TrimSpace(clientURL)}
	case strings.TrimSpace(clientVer) != "":
		return proto.ServiceRelease{Version: strings.TrimSpace(clientVer)}
	case strings.TrimSpace(fleetURL) != "":
		return proto.ServiceRelease{DownloadURL: strings.TrimSpace(fleetURL)}
	default:
		// May be empty, on a development build of this server. The client installs
		// nothing in that case rather than guessing at a release.
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
		Force: force,
	}
}

// pushServicesConfig tells one host which builds to run.
//
// force is the difference between the two callers. Unforced, the host installs
// only what it is missing, which is what makes this safe to send on every connect
// -- and it has to be safe, because a version left empty means "the control
// server's own", so an upgrade here would fire across the whole fleet every time
// the control server was deployed. Forced is an operator pressing the button on
// one host, having been told what it does.
//
// Failures are logged rather than returned, for the same reason the other pushes
// do it: an offline host is the normal case, and it is told as soon as it comes
// back.
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

// recordInstalledVersions stores what a host says its companions actually are.
//
// A nil report is left alone rather than written as empty: that is what a client
// too old to report them sends, and blanking the row on every connect from one
// would be worse than showing nothing at all. An empty field inside a report that
// did arrive is written through -- the host looked and found nothing.
//
// Best-effort, like the facts update beside it: this is a report arriving, and
// failing to store it is not a reason to refuse the connection.
func recordInstalledVersions(ctx context.Context, q *db.Queries, id pgtype.UUID, clientID string, st *proto.ServicesState) {
	if st == nil {
		return
	}
	if err := q.UpdateClientInstalledVersions(ctx, db.UpdateClientInstalledVersionsParams{
		ID:                    id,
		DpipeInstalledVersion: st.Dpipe,
		ProxyInstalledVersion: st.Proxy,
	}); err != nil {
		log.Printf("client %s: could not record the installed versions: %v", clientID, err)
	}
}
