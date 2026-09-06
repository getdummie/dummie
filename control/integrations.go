package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type IntegrationHandler struct {
	q          *db.Queries
	pool       *pgxpool.Pool
	controlURL string
}

type integrationDTO struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Account      string   `json:"account"`
	Connected    bool     `json:"connected"`
	Suspended    bool     `json:"suspended"`
	AllRepos     bool     `json:"all_repos"`
	AllReposable bool     `json:"all_reposable"`
	Readonly     bool     `json:"readonly"`
	RepoCount    int64    `json:"repo_count"`
	VMCount      int64    `json:"vm_count"`
	Repos        []string `json:"repos,omitempty"`
	CreatedAt    string   `json:"created_at"`
}

// @Summary     Integrations you own
// @Tags        integrations
// @Produce     json
// @Router      /integrations [get]
func (h *IntegrationHandler) List(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return err
	}
	rows, err := h.q.ListIntegrationsByOwner(c.Request().Context(), owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list integrations")
	}
	items := make([]integrationDTO, 0, len(rows))
	for _, r := range rows {
		items = append(items, integrationDTO{
			ID:           domainIDString(r.ID),
			Name:         r.Name,
			Kind:         r.Kind,
			Account:      r.AccountLogin.String,
			Connected:    r.InstallationPk.Valid,
			Suspended:    r.Suspended.Bool,
			AllRepos:     r.AllRepos,
			AllReposable: r.RepositorySelection.String == "all",
			Readonly:     r.Readonly,
			RepoCount:    r.RepoCount,
			VMCount:      r.VmCount,
			CreatedAt:    r.CreatedAt.Time.Format(time.RFC3339),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type createIntegrationReq struct {
	Name     string `json:"name"`
	Readonly bool   `json:"readonly"`
}

// @Summary     Create an integration
// @Tags        integrations
// @Accept      json
// @Produce     json
// @Router      /integrations [post]
func (h *IntegrationHandler) Create(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return err
	}
	var req createIntegrationReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if err := validateIntegrationName(name); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	row, err := h.q.CreateIntegration(c.Request().Context(), db.CreateIntegrationParams{
		OwnerID:  owner,
		Name:     name,
		Kind:     integrationKindGitHub,
		Readonly: req.Readonly,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "you already have an integration with that name")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create the integration")
	}

	return c.JSON(http.StatusCreated, integrationDTO{
		ID:        domainIDString(row.ID),
		Name:      row.Name,
		Kind:      row.Kind,
		Readonly:  row.Readonly,
		CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
	})
}

// @Summary     One integration
// @Tags        integrations
// @Produce     json
// @Router      /integrations/{id} [get]
func (h *IntegrationHandler) Get(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}

	repos, err := h.q.ListIntegrationRepos(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the repositories")
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.RepoOwner+"/"+r.RepoName)
	}

	return c.JSON(http.StatusOK, integrationDTO{
		ID:           domainIDString(row.ID),
		Name:         row.Name,
		Kind:         row.Kind,
		Account:      row.AccountLogin.String,
		Connected:    row.InstallationPk.Valid,
		Suspended:    row.Suspended.Bool,
		AllRepos:     row.AllRepos,
		AllReposable: row.RepositorySelection.String == "all",
		Readonly:     row.Readonly,
		RepoCount:    int64(len(names)),
		Repos:        names,
		CreatedAt:    row.CreatedAt.Time.Format(time.RFC3339),
	})
}

type updateIntegrationReq struct {
	AllRepos bool `json:"all_repos"`
	Readonly bool `json:"readonly"`
}

// @Summary     Update an integration's flags
// @Tags        integrations
// @Accept      json
// @Produce     json
// @Router      /integrations/{id} [patch]
func (h *IntegrationHandler) Update(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	var req updateIntegrationReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	ctx := c.Request().Context()
	current, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}
	// "Every repository" only means anything when github itself granted the
	// installation every repository.
	if req.AllRepos && current.RepositorySelection.String != "all" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"this installation is limited to selected repositories on github, so it cannot cover all of them")
	}

	if _, err := h.q.UpdateIntegrationFlags(ctx, db.UpdateIntegrationFlagsParams{
		ID:       id,
		AllRepos: req.AllRepos,
		Readonly: req.Readonly,
		OwnerID:  owner,
	}); err != nil {
		return notFoundOr(err, "no such integration", "could not update the integration")
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Delete an integration
// @Tags        integrations
// @Router      /integrations/{id} [delete]
func (h *IntegrationHandler) Delete(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	if err := h.q.DeleteIntegrationForOwner(c.Request().Context(), db.DeleteIntegrationForOwnerParams{
		ID: id, OwnerID: owner,
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the integration")
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Repositories this installation could cover
// @Tags        integrations
// @Produce     json
// @Router      /integrations/{id}/available-repos [get]
func (h *IntegrationHandler) ListAvailableRepos(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}
	if !row.InstallationPk.Valid {
		return echo.NewHTTPError(http.StatusConflict, "connect a github account to this integration first")
	}

	app, err := h.githubApp(ctx)
	if err != nil {
		return err
	}
	repos, err := app.listInstallationRepos(ctx, row.InstallationID.Int64)
	if err != nil {
		log.Printf("could not list the repositories of installation %d: %v", row.InstallationID.Int64, err)
		return echo.NewHTTPError(http.StatusBadGateway, "github could not be asked which repositories this covers")
	}

	items := make([]string, 0, len(repos))
	for _, r := range repos {
		items = append(items, r.Owner+"/"+r.Name)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type setReposReq struct {
	Repos []string `json:"repos"`
}

// @Summary     Set which repositories an integration covers
// @Tags        integrations
// @Accept      json
// @Router      /integrations/{id}/repos [put]
func (h *IntegrationHandler) SetRepos(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	var req setReposReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	type repo struct{ owner, name string }
	seen := map[repo]bool{}
	var repos []repo
	for _, raw := range req.Repos {
		parts := strings.Split(strings.TrimSpace(raw), "/")
		if len(parts) != 2 || !githubNameRe.MatchString(parts[0]) || !githubNameRe.MatchString(parts[1]) {
			return echo.NewHTTPError(http.StatusBadRequest, "each repository must be owner/name: "+raw)
		}
		r := repo{owner: parts[0], name: parts[1]}
		if seen[r] {
			continue
		}
		seen[r] = true
		repos = append(repos, r)
	}

	ctx := c.Request().Context()
	if _, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner}); err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}

	// Replaced as a unit: a half-applied list would silently widen or narrow
	// what the attached VMs can reach.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.q.WithTx(tx)

	if err := qtx.DeleteIntegrationRepos(ctx, id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	for _, r := range repos {
		if err := qtx.InsertIntegrationRepo(ctx, db.InsertIntegrationRepoParams{
			IntegrationID: id,
			RepoOwner:     r.owner,
			RepoName:      r.name,
		}); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	return c.NoContent(http.StatusNoContent)
}

type integrationVMDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// @Summary     VMs an integration is attached to
// @Tags        integrations
// @Produce     json
// @Router      /integrations/{id}/vms [get]
func (h *IntegrationHandler) ListVMs(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if _, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner}); err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}
	rows, err := h.q.ListIntegrationVMs(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list the attached vms")
	}
	items := make([]integrationVMDTO, 0, len(rows))
	for _, r := range rows {
		items = append(items, integrationVMDTO{ID: domainIDString(r.ID), Name: r.Name, IP: r.IP})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// Attach joins two owned objects, so both owners are checked: the integration's
// and the vm's. Either check alone would be insufficient.
//
// @Summary     Attach an integration to a vm
// @Tags        integrations
// @Router      /integrations/{id}/vms/{vm_id} [post]
func (h *IntegrationHandler) Attach(c *echo.Context) error {
	owner, id, vmID, err := h.ownedIntegrationAndVM(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}
	if !row.InstallationPk.Valid {
		return echo.NewHTTPError(http.StatusConflict, "connect a github account to this integration first")
	}

	if err := h.q.AttachIntegrationToVM(ctx, db.AttachIntegrationToVMParams{
		VMID: vmID, IntegrationID: id,
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not attach the integration")
	}
	// No config push: the pushed intproxy config carries no policy, so the next
	// broker call sees this. Do not "fix" this by adding one.
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Detach an integration from a vm
// @Tags        integrations
// @Router      /integrations/{id}/vms/{vm_id} [delete]
func (h *IntegrationHandler) Detach(c *echo.Context) error {
	_, id, vmID, err := h.ownedIntegrationAndVM(c)
	if err != nil {
		return err
	}
	if err := h.q.DetachIntegrationFromVM(c.Request().Context(), db.DetachIntegrationFromVMParams{
		VMID: vmID, IntegrationID: id,
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not detach the integration")
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *IntegrationHandler) ownedIntegration(c *echo.Context) (pgtype.UUID, pgtype.UUID, error) {
	owner, err := callerID(c)
	if err != nil {
		return owner, pgtype.UUID{}, err
	}
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return owner, id, echo.NewHTTPError(http.StatusBadRequest, "invalid integration id")
	}
	return owner, id, nil
}

func (h *IntegrationHandler) ownedIntegrationAndVM(c *echo.Context) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return owner, id, pgtype.UUID{}, err
	}
	vmID, err := parseUUID(c.Param("vm_id"))
	if err != nil {
		return owner, id, vmID, echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}

	if _, err := h.q.GetVMForOwner(c.Request().Context(), db.GetVMForOwnerParams{
		ID: vmID, CreatedBy: owner,
	}); err != nil {
		return owner, id, vmID, notFoundOr(err, "no such vm", "could not read the vm")
	}
	return owner, id, vmID, nil
}

func (h *IntegrationHandler) githubApp(ctx context.Context) (*githubApp, error) {
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

// integrationNameShapeRe mirrors the integrations_name_shape check constraint,
// so a bad name is a 400 rather than a 500 from postgres.
var integrationNameShapeRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validateIntegrationName(name string) error {
	switch {
	case len(name) < 3 || len(name) > 52:
		return errors.New("the name must be between 3 and 52 characters")
	case !integrationNameShapeRe.MatchString(name):
		return errors.New("the name may hold lowercase letters, digits and single dashes")
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func notFoundOr(err error, notFound, other string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, notFound)
	}
	return echo.NewHTTPError(http.StatusInternalServerError, other)
}
