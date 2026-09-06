package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

const integrationKindGitHub = "github"

var githubNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// IntegrationToken mints a github token for a vm. This is the authorization
// boundary for the whole feature: the calling host is identified by its own
// bearer token, and the vm is only ever looked up among that host's own vms.
func (h *ClientHandler) IntegrationToken(c *echo.Context) error {
	client, err := h.authClient(c)
	if err != nil {
		return err
	}

	var req proto.IntegrationTokenRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "could not decode the request")
	}
	if req.Integration != integrationKindGitHub {
		return echo.NewHTTPError(http.StatusBadRequest, "unknown integration kind")
	}
	ip := net.ParseIP(strings.TrimSpace(req.VMIP))
	if ip == nil || ip.To4() == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "vm_ip is not an IPv4 address")
	}

	var owner, repo string
	if req.Repo != "" {
		parts := strings.Split(req.Repo, "/")
		if len(parts) != 2 || !githubNameRe.MatchString(parts[0]) || !githubNameRe.MatchString(parts[1]) {
			return echo.NewHTTPError(http.StatusBadRequest, "repo must be owner/name")
		}
		owner, repo = parts[0], parts[1]
	}

	ctx := c.Request().Context()
	grants, err := h.q.ListIntegrationGrantsForVM(ctx, db.ListIntegrationGrantsForVMParams{
		Kind:     integrationKindGitHub,
		ClientID: client.ID,
		VMIP:     ip.String(),
	})
	if err != nil {
		log.Printf("could not read the integrations for %s on client %s: %v", ip, clientLabel(client), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the integrations for this vm")
	}

	decision := h.pickGrant(ctx, grants, owner, repo, req.Write)
	if decision.err != nil {
		log.Printf("integration denied: host=%s vm_ip=%s repo=%s write=%t reason=%s",
			clientLabel(client), ip, req.Repo, req.Write, decision.reason)
		return decision.err
	}

	app, err := h.githubApp(ctx)
	if err != nil {
		return err
	}

	repos := decision.repos
	tok, err := h.tokens.get(ctx, app, decision.installationID, repos, req.Write)
	if err != nil {
		return h.githubMintError(ctx, decision.installationPK, err)
	}

	log.Printf("integration allowed: host=%s vm=%s integration=%s repo=%s write=%t",
		clientLabel(client), decision.vmName, decision.integrationName, req.Repo, req.Write)

	return c.JSON(http.StatusOK, proto.IntegrationTokenResponse{
		Token:     tok.Token,
		ExpiresAt: tok.ExpiresAt,
		Account:   decision.account,
	})
}

type grantDecision struct {
	installationID  int64
	installationPK  pgtype.UUID
	integrationName string
	account         string
	vmName          string
	repos           []string

	err    error
	reason string
}

// pickGrant is the decision itself. A request naming a repository needs an
// attached integration that covers exactly that repository; one that names none
// -- graphql, and rest outside /repos -- gets a token scoped to every
// repository the vm's integrations cover, which is coarser but still bounded.
func (h *ClientHandler) pickGrant(ctx context.Context, grants []db.ListIntegrationGrantsForVMRow, owner, repo string, write bool) grantDecision {
	if len(grants) == 0 {
		return grantDecision{
			reason: "no integration is attached to this vm",
			err: echo.NewHTTPError(http.StatusForbidden,
				"this vm is not attached to a github integration"),
		}
	}

	var sawUsable, sawReadonly bool
	for _, g := range grants {
		if g.Suspended {
			continue
		}
		sawUsable = true
		if write && g.Readonly {
			sawReadonly = true
			continue
		}

		allowed := g.AllRepos
		if !allowed && repo != "" {
			ok, err := h.q.IntegrationAllowsRepo(ctx, db.IntegrationAllowsRepoParams{
				IntegrationID: g.IntegrationID,
				RepoOwner:     owner,
				RepoName:      repo,
			})
			if err != nil {
				log.Printf("could not check the repositories of integration %s: %v", g.IntegrationName, err)
				continue
			}
			allowed = ok
		}
		if !allowed && repo != "" {
			continue
		}

		d := grantDecision{
			installationID:  g.InstallationID,
			installationPK:  g.InstallationPk,
			integrationName: g.IntegrationName,
			account:         g.AccountLogin,
			vmName:          g.VMName,
		}
		switch {
		case repo != "":
			d.repos = []string{repo}
		case g.AllRepos:
			// No repositories field at all: the token covers the installation.
		default:
			d.repos = h.reposForIntegration(ctx, g.IntegrationID)
			if len(d.repos) == 0 {
				continue
			}
		}
		return d
	}

	switch {
	case !sawUsable:
		return grantDecision{
			reason: "every attached integration is suspended on github",
			err: echo.NewHTTPError(http.StatusConflict,
				"this vm's github integration was removed or suspended on github; reconnect it"),
		}
	case sawReadonly:
		return grantDecision{
			reason: "write refused on a read-only integration",
			err: echo.NewHTTPError(http.StatusForbidden,
				"this integration is read-only, so it cannot push"),
		}
	}
	return grantDecision{
		reason: "no attached integration covers the repository",
		err: echo.NewHTTPError(http.StatusForbidden,
			"no integration attached to this vm covers that repository"),
	}
}

func (h *ClientHandler) reposForIntegration(ctx context.Context, id pgtype.UUID) []string {
	rows, err := h.q.ListIntegrationRepos(ctx, id)
	if err != nil {
		log.Printf("could not list the repositories of an integration: %v", err)
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.RepoName)
	}
	return out
}

func (h *ClientHandler) githubApp(ctx context.Context) (*githubApp, error) {
	row, err := h.q.GetGitHubApp(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, echo.NewHTTPError(http.StatusServiceUnavailable,
			"no github app is configured on this control server")
	}
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "could not read the github app")
	}
	app, err := parseGitHubApp(row.AppID, row.PrivateKey)
	if err != nil {
		log.Printf("the configured github app is unusable: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "the github app is not usable")
	}
	return app, nil
}

// githubMintError maps github's own status onto ours, and records the ones that
// mean the installation is gone so the console can say "reconnect" instead of
// every clone failing with nothing to act on.
func (h *ClientHandler) githubMintError(ctx context.Context, installationPK pgtype.UUID, err error) error {
	var ghErr *githubStatusError
	if !errors.As(err, &ghErr) {
		log.Printf("could not mint a github token: %v", err)
		return echo.NewHTTPError(http.StatusGatewayTimeout, "github could not be reached")
	}

	switch ghErr.status {
	case http.StatusNotFound, http.StatusGone:
		h.suspendInstallation(ctx, installationPK, ghErr.message)
		return echo.NewHTTPError(http.StatusConflict,
			"this github installation no longer exists; reconnect it")
	case http.StatusUnauthorized:
		h.suspendInstallation(ctx, installationPK, ghErr.message)
		log.Printf("github rejected the app credentials: %v", ghErr)
		return echo.NewHTTPError(http.StatusBadGateway, "github rejected this control server's app credentials")
	case http.StatusForbidden:
		if ghErr.retryAfter != "" {
			return echo.NewHTTPError(http.StatusTooManyRequests, "github is rate limiting this integration")
		}
		return echo.NewHTTPError(http.StatusForbidden, ghErr.message)
	case http.StatusUnprocessableEntity:
		return echo.NewHTTPError(http.StatusForbidden,
			"that repository is not part of this github installation")
	}
	log.Printf("could not mint a github token: %v", ghErr)
	return echo.NewHTTPError(http.StatusBadGateway, "github could not issue a token")
}

func (h *ClientHandler) suspendInstallation(ctx context.Context, id pgtype.UUID, reason string) {
	if err := h.q.SetInstallationSuspended(ctx, db.SetInstallationSuspendedParams{
		ID:        id,
		Suspended: true,
		LastError: reason,
	}); err != nil {
		log.Printf("could not record that a github installation is suspended: %v", err)
	}
}

func clientLabel(client db.Client) string {
	if client.Hostname != "" {
		return client.Hostname
	}
	return client.MachineID
}
