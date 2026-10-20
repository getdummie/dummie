package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

const integrationKindGitLab = "gitlab"

// Instance flavors. Each is also the intproxy kind serving that instance;
// gitea stands for forgejo as well.
const (
	gitFlavorGitLab = "gitlab"
	gitFlavorGitea  = "gitea"
)

// gitlabNameRe mirrors gitlab_instances_name_shape. The name is a hostname
// label under int.<tld>, next to the built-in github and llm.
var gitlabNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validateGitLabName(name string) error {
	switch {
	case len(name) < 2 || len(name) > 63:
		return errors.New("the name must be between 2 and 63 characters")
	case !gitlabNameRe.MatchString(name):
		return errors.New("the name may hold lowercase letters, digits and single dashes")
	case name == integrationKindGitHub || name == integrationKindLLM:
		return errors.New("that name is taken by a built-in integration")
	}
	return nil
}

// normalizeGitLabURL holds the base to what intproxy's upstreamBase accepts,
// so a url that saves here is one the proxy will actually use.
func normalizeGitLabURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the url must be https://host[/path], with nothing else")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

type gitlabInstanceDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Flavor    string `json:"flavor"`
	UpdatedAt string `json:"updated_at"`
}

func toGitLabInstanceDTO(g db.GitlabInstance) gitlabInstanceDTO {
	return gitlabInstanceDTO{
		ID:        domainIDString(g.ID),
		Name:      g.Name,
		BaseURL:   g.BaseURL,
		Flavor:    g.Flavor,
		UpdatedAt: g.UpdatedAt.Time.Format(time.RFC3339),
	}
}

func listGitLabInstances(c *echo.Context, q *db.Queries) error {
	rows, err := q.ListGitLabInstances(c.Request().Context())
	if err != nil {
		log.Printf("could not list gitlab instances: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list gitlab instances")
	}
	items := make([]gitlabInstanceDTO, 0, len(rows))
	for _, r := range rows {
		items = append(items, toGitLabInstanceDTO(r))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// @Summary     GitLab instances reachable through integrations
// @Tags        admin
// @Produce     json
// @Router      /admin/gitlab-instances [get]
func (h *AdminHandler) ListGitLabInstances(c *echo.Context) error {
	return listGitLabInstances(c, h.q)
}

type gitlabInstanceReq struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Flavor  string `json:"flavor"`
}

// @Summary     Add a gitlab instance
// @Tags        admin
// @Accept      json
// @Produce     json
// @Router      /admin/gitlab-instances [post]
func (h *AdminHandler) CreateGitLabInstance(c *echo.Context) error {
	uid, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	var req gitlabInstanceReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if err := validateGitLabName(name); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	base, err := normalizeGitLabURL(req.BaseURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	flavor := req.Flavor
	switch flavor {
	case "":
		flavor = gitFlavorGitLab
	case gitFlavorGitLab, gitFlavorGitea:
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "flavor must be gitlab or gitea")
	}

	ctx := c.Request().Context()
	row, err := h.q.CreateGitLabInstance(ctx, db.CreateGitLabInstanceParams{
		Name: name, BaseURL: base, Flavor: flavor, UpdatedBy: uid,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "an instance with that name already exists")
		}
		log.Printf("could not create gitlab instance %s: %v", name, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the gitlab instance")
	}
	h.pushIntproxyConfigToAll(ctx)
	return c.JSON(http.StatusCreated, toGitLabInstanceDTO(row))
}

// @Summary     Change a gitlab instance's url
// @Tags        admin
// @Accept      json
// @Produce     json
// @Router      /admin/gitlab-instances/{id} [patch]
func (h *AdminHandler) UpdateGitLabInstance(c *echo.Context) error {
	uid, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gitlab instance id")
	}
	var req gitlabInstanceReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	base, err := normalizeGitLabURL(req.BaseURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	row, err := h.q.UpdateGitLabInstance(c.Request().Context(), db.UpdateGitLabInstanceParams{
		ID: id, BaseURL: base, UpdatedBy: uid,
	})
	if err != nil {
		return notFoundOr(err, "no such gitlab instance", "could not save the gitlab instance")
	}
	return c.JSON(http.StatusOK, toGitLabInstanceDTO(row))
}

// @Summary     Remove a gitlab instance and every integration using it
// @Tags        admin
// @Success     204
// @Router      /admin/gitlab-instances/{id} [delete]
func (h *AdminHandler) DeleteGitLabInstance(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gitlab instance id")
	}
	ctx := c.Request().Context()
	n, err := h.q.DeleteGitLabInstance(ctx, id)
	if err != nil {
		log.Printf("could not delete gitlab instance %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the gitlab instance")
	}
	if n > 0 {
		h.pushIntproxyConfigToAll(ctx)
	}
	return c.NoContent(http.StatusNoContent)
}

// pushIntproxyConfigToAll is how a new or removed instance reaches the hosts:
// the set of names intproxy answers for is part of its config.
func (h *AdminHandler) pushIntproxyConfigToAll(ctx context.Context) {
	for _, raw := range h.hub.ConnectedIDs() {
		id, err := parseUUID(raw)
		if err != nil {
			continue
		}
		pushIntproxyConfig(ctx, h.q, h.blobs, h.hub, h.controlURL, id)
	}
}

// @Summary     GitLab instances a gitlab integration can use
// @Tags        integrations
// @Produce     json
// @Router      /integrations/gitlab-instances [get]
func (h *IntegrationHandler) ListGitLabInstances(c *echo.Context) error {
	return listGitLabInstances(c, h.q)
}

func gitlabTokenAAD(owner, instance pgtype.UUID) []byte {
	return []byte("gitlab|" + domainIDString(owner) + "|" + domainIDString(instance))
}

var gitlabHTTPClient = &http.Client{Timeout: 15 * time.Second}

var (
	errGitLabTokenRejected = errors.New("gitlab rejected this token")
	errGitLabNoAPIScope    = errors.New("the token may not call the gitlab api")
	errGitLabUnreachable   = errors.New("gitlab could not be reached")
)

type gitlabTokenInfo struct {
	Username string
}

func checkGitToken(ctx context.Context, flavor, base, token string, readonly bool) (gitlabTokenInfo, error) {
	if flavor == gitFlavorGitea {
		return checkGiteaToken(ctx, base, token)
	}
	return checkGitLabToken(ctx, base, token, readonly)
}

// checkGiteaToken only proves the token is real: gitea has no endpoint that
// reports a token's own scopes, and a repository-only token cannot read /user.
func checkGiteaToken(ctx context.Context, base, token string) (gitlabTokenInfo, error) {
	var user struct {
		Login string `json:"login"`
	}
	err := gitlabGet(ctx, base+"/api/v1/user", "Authorization", "token "+token, &user)
	if errors.Is(err, errGitLabNoAPIScope) {
		return gitlabTokenInfo{}, nil
	}
	if err != nil {
		return gitlabTokenInfo{}, err
	}
	return gitlabTokenInfo{Username: user.Login}, nil
}

// checkGitLabToken refuses a token that could not do what it is stored for.
// A repository-only token cannot read its own scopes, so it is taken on trust.
func checkGitLabToken(ctx context.Context, base, token string, readonly bool) (gitlabTokenInfo, error) {
	var self struct {
		Active bool     `json:"active"`
		Scopes []string `json:"scopes"`
	}
	err := gitlabGet(ctx, base+"/api/v4/personal_access_tokens/self", "PRIVATE-TOKEN", token, &self)
	if errors.Is(err, errGitLabNoAPIScope) {
		return gitlabTokenInfo{}, nil
	}
	if err != nil {
		return gitlabTokenInfo{}, err
	}
	if !self.Active {
		return gitlabTokenInfo{}, errors.New("this token is revoked or expired")
	}
	if !slices.Contains(self.Scopes, "read_repository") && !slices.Contains(self.Scopes, "api") {
		return gitlabTokenInfo{}, errors.New("the token needs the read_repository scope")
	}
	if !readonly && !slices.Contains(self.Scopes, "write_repository") && !slices.Contains(self.Scopes, "api") {
		return gitlabTokenInfo{}, errors.New("the token needs the write_repository scope, or make the integration read-only")
	}

	var user struct {
		Username string `json:"username"`
	}
	// read_repository alone cannot read /user, and the name is only shown.
	_ = gitlabGet(ctx, base+"/api/v4/user", "PRIVATE-TOKEN", token, &user)
	return gitlabTokenInfo{Username: user.Username}, nil
}

func gitlabGet(ctx context.Context, u, header, value string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set(header, value)
	resp, err := gitlabHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errGitLabUnreachable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		log.Printf("a token was refused at %s: %s", u, bytes.TrimSpace(raw))
		return errGitLabTokenRejected
	case resp.StatusCode == http.StatusForbidden:
		return errGitLabNoAPIScope
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: it answered %d", errGitLabUnreachable, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %v", errGitLabUnreachable, err)
	}
	return nil
}

// gitlabCheckError tells a rejected or under-scoped token apart from gitlab
// being unreachable.
func gitlabCheckError(err error) error {
	switch {
	case errors.Is(err, errGitLabTokenRejected):
		return echo.NewHTTPError(http.StatusBadRequest, "the instance rejected this token")
	case errors.Is(err, errGitLabUnreachable):
		log.Printf("could not check a git instance token: %v", err)
		return echo.NewHTTPError(http.StatusBadGateway, "could not reach the instance to check this token")
	}
	return echo.NewHTTPError(http.StatusBadRequest, err.Error())
}

type createGitLabIntegrationReq struct {
	Name       string `json:"name"`
	InstanceID string `json:"instance_id"`
	Token      string `json:"token"`
	Readonly   bool   `json:"readonly"`
}

func (h *IntegrationHandler) createGitLab(c *echo.Context, owner pgtype.UUID, req createGitLabIntegrationReq) error {
	if h.sealer == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "gitlab integrations are not enabled on this server")
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if err := validateIntegrationName(name); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	instanceID, err := parseUUID(req.InstanceID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gitlab instance id")
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "a personal access token is required")
	}

	ctx := c.Request().Context()
	inst, err := h.q.GetGitLabInstance(ctx, instanceID)
	if err != nil {
		return notFoundOr(err, "no such gitlab instance", "could not read the gitlab instance")
	}
	info, err := checkGitToken(ctx, inst.Flavor, inst.BaseURL, token, req.Readonly)
	if err != nil {
		return gitlabCheckError(err)
	}
	sealed, err := h.sealer.seal(token, gitlabTokenAAD(owner, inst.ID))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not encrypt the token")
	}

	row, err := h.q.CreateGitLabIntegration(ctx, db.CreateGitLabIntegrationParams{
		OwnerID:          owner,
		Name:             name,
		Readonly:         req.Readonly,
		GitLabInstanceID: inst.ID,
		TokenEnc:         sealed,
		GitLabUsername:   info.Username,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "you already have an integration with that name")
		}
		log.Printf("could not create a gitlab integration for %s: %v", domainIDString(owner), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create the integration")
	}
	return c.JSON(http.StatusCreated, integrationDTO{
		ID:        domainIDString(row.ID),
		Name:      row.Name,
		Kind:      row.Kind,
		Account:   row.GitLabUsername,
		Connected: true,
		Readonly:  row.Readonly,
		Instance:  inst.Name,
		BaseURL:   inst.BaseURL,
		Flavor:    inst.Flavor,
		CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
	})
}

type setGitLabTokenReq struct {
	Token string `json:"token"`
}

// @Summary     Replace a gitlab integration's personal access token
// @Tags        integrations
// @Accept      json
// @Router      /integrations/{id}/gitlab/token [put]
func (h *IntegrationHandler) SetGitLabToken(c *echo.Context) error {
	if h.sealer == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "gitlab integrations are not enabled on this server")
	}
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	var req setGitLabTokenReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "a personal access token is required")
	}

	ctx := c.Request().Context()
	row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}
	if row.Kind != integrationKindGitLab {
		return echo.NewHTTPError(http.StatusBadRequest, "this is not a gitlab integration")
	}
	info, err := checkGitToken(ctx, row.GitLabFlavor.String, row.GitLabBaseURL.String, token, row.Readonly)
	if err != nil {
		return gitlabCheckError(err)
	}
	sealed, err := h.sealer.seal(token, gitlabTokenAAD(owner, row.GitLabInstanceID))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not encrypt the token")
	}
	if _, err := h.q.UpdateGitLabIntegrationToken(ctx, db.UpdateGitLabIntegrationTokenParams{
		ID: id, OwnerID: owner, TokenEnc: sealed, GitLabUsername: info.Username,
	}); err != nil {
		log.Printf("could not store the token of integration %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the token")
	}
	return c.NoContent(http.StatusNoContent)
}

// gitlabToken answers the broker for a gitlab instance. An attachment grants
// whatever the token's own scopes reach; readonly only refuses pushes.
func (h *ClientHandler) gitlabToken(c *echo.Context, client db.Client, inst db.GitlabInstance, ip string, req proto.IntegrationTokenRequest) error {
	if h.llmSealer == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "gitlab integrations are not enabled on the control server")
	}
	ctx := c.Request().Context()
	grants, err := h.q.ListGitLabGrantsForVM(ctx, db.ListGitLabGrantsForVMParams{
		ClientID: client.ID, VMIP: ip, InstanceName: inst.Name,
	})
	if err != nil {
		log.Printf("could not read the gitlab integrations for %s on client %s: %v", ip, clientLabel(client), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the integrations for this vm")
	}

	var pick *db.ListGitLabGrantsForVMRow
	for i := range grants {
		if req.Write && grants[i].Readonly {
			continue
		}
		pick = &grants[i]
		break
	}
	if pick == nil {
		reason := "no " + inst.Name + " integration is attached to this vm"
		if len(grants) > 0 {
			reason = "every " + inst.Name + " integration attached to this vm is read-only"
		}
		log.Printf("integration denied: host=%s vm_ip=%s integration=%s repo=%s write=%t reason=%s",
			clientLabel(client), ip, inst.Name, req.Resource, req.Write, reason)
		return echo.NewHTTPError(http.StatusForbidden, reason)
	}

	token, err := h.llmSealer.open(pick.TokenEnc, gitlabTokenAAD(pick.OwnerID, inst.ID))
	if err != nil {
		log.Printf("could not open the token of integration %s: %v", domainIDString(pick.IntegrationID), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "the stored token could not be decrypted; set it again")
	}

	log.Printf("integration allowed: host=%s vm=%s integration=%s repo=%s write=%t",
		clientLabel(client), pick.VMName, pick.IntegrationName, req.Resource, req.Write)
	return c.JSON(http.StatusOK, proto.IntegrationTokenResponse{
		Token:     token,
		ExpiresAt: time.Now().Add(time.Hour),
		Upstream:  pick.BaseURL,
	})
}

func lookupGitLabInstance(ctx context.Context, q *db.Queries, name string) (db.GitlabInstance, bool, error) {
	inst, err := q.GetGitLabInstanceByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return inst, false, nil
	}
	return inst, err == nil, err
}
