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
		log.Printf("could not list the integrations of %s: %v", domainIDString(owner), err)
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
		log.Printf("could not create an integration for %s: %v", domainIDString(owner), err)
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
		log.Printf("could not read the repositories of integration %s: %v", domainIDString(id), err)
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
		log.Printf("could not delete integration %s: %v", domainIDString(id), err)
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

	repos, err := parseRepoList(req.Repos)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ctx := c.Request().Context()
	if _, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner}); err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}

	// Replaced as a unit: a half-applied list would silently widen or narrow
	// what the attached VMs can reach.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		log.Printf("could not begin a transaction to save repositories: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.q.WithTx(tx)

	if err := qtx.DeleteIntegrationRepos(ctx, id); err != nil {
		log.Printf("could not clear the repositories of integration %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	for _, r := range repos {
		if err := qtx.InsertIntegrationRepo(ctx, db.InsertIntegrationRepoParams{
			IntegrationID: id,
			RepoOwner:     r.owner,
			RepoName:      r.name,
		}); err != nil {
			log.Printf("could not add %s/%s to integration %s: %v", r.owner, r.name, domainIDString(id), err)
			return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("could not commit the repositories of integration %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	return c.NoContent(http.StatusNoContent)
}

type integrationVMDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`

	// Repos empty means this attachment inherits the integration's whole list.
	Repos []string `json:"repos"`
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
		log.Printf("could not list the vms of integration %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list the attached vms")
	}
	items := make([]integrationVMDTO, 0, len(rows))
	for _, r := range rows {
		scoped, err := h.q.ListVMIntegrationRepos(ctx, db.ListVMIntegrationReposParams{
			VMID: r.ID, IntegrationID: id,
		})
		if err != nil {
			log.Printf("could not read the repository scope of integration %s on vm %s: %v",
				domainIDString(id), r.Name, err)
			return echo.NewHTTPError(http.StatusInternalServerError, "could not read the attached vms")
		}
		repos := make([]string, 0, len(scoped))
		for _, s := range scoped {
			repos = append(repos, s.RepoOwner+"/"+s.RepoName)
		}
		items = append(items, integrationVMDTO{
			ID: domainIDString(r.ID), Name: r.Name, IP: r.IP, Repos: repos,
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// SetVMRepos narrows one attachment to a subset of the integration's
// repositories. An empty list clears the scoping, which means the attachment
// inherits the integration's whole list again.
//
// @Summary     Limit which of an integration's repositories one vm may reach
// @Tags        integrations
// @Accept      json
// @Router      /integrations/{id}/vms/{vm_id}/repos [put]
func (h *IntegrationHandler) SetVMRepos(c *echo.Context) error {
	owner, id, vmID, err := h.ownedIntegrationAndVM(c)
	if err != nil {
		return err
	}
	var req setReposReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	ctx := c.Request().Context()
	row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}

	repos, err := parseRepoList(req.Repos)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// A scope naming something the integration itself does not cover would be
	// silently ineffective, so refuse it rather than store a lie.
	if !row.AllRepos {
		covered, err := h.q.ListIntegrationRepos(ctx, id)
		if err != nil {
			log.Printf("could not read the repositories of integration %s: %v", domainIDString(id), err)
			return echo.NewHTTPError(http.StatusInternalServerError, "could not read the repositories")
		}
		allowed := map[string]bool{}
		for _, r := range covered {
			allowed[strings.ToLower(r.RepoOwner+"/"+r.RepoName)] = true
		}
		for _, r := range repos {
			if !allowed[strings.ToLower(r.owner+"/"+r.name)] {
				return echo.NewHTTPError(http.StatusBadRequest,
					r.owner+"/"+r.name+" is not one of this integration's repositories")
			}
		}
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		log.Printf("could not begin a transaction to scope an attachment: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.q.WithTx(tx)

	if err := qtx.DeleteVMIntegrationRepos(ctx, db.DeleteVMIntegrationReposParams{
		VMID: vmID, IntegrationID: id,
	}); err != nil {
		log.Printf("could not clear the repository scope of integration %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	for _, r := range repos {
		if err := qtx.InsertVMIntegrationRepo(ctx, db.InsertVMIntegrationRepoParams{
			VMID:          vmID,
			IntegrationID: id,
			RepoOwner:     r.owner,
			RepoName:      r.name,
		}); err != nil {
			log.Printf("could not scope %s/%s to a vm: %v", r.owner, r.name, err)
			return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("could not commit the repository scope of integration %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the repositories")
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Integrations attached to one of your VMs
// @Tags        integrations
// @Produce     json
// @Router      /vms/{id}/integrations [get]
func (h *IntegrationHandler) ListForVM(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return err
	}
	vmID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}

	ctx := c.Request().Context()
	if _, err := h.q.GetVMForOwner(ctx, db.GetVMForOwnerParams{ID: vmID, CreatedBy: owner}); err != nil {
		return notFoundOr(err, "no such vm", "could not read the vm")
	}

	rows, err := h.q.ListVMIntegrationsForOwner(ctx, db.ListVMIntegrationsForOwnerParams{
		VMID: vmID, OwnerID: owner,
	})
	if err != nil {
		log.Printf("could not list the integrations of vm %s: %v", domainIDString(vmID), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list the integrations")
	}
	items := make([]integrationDTO, 0, len(rows))
	for _, r := range rows {
		dto := integrationDTO{
			ID:       domainIDString(r.ID),
			Name:     r.Name,
			Readonly: r.Readonly,
			AllRepos: r.AllRepos,
		}
		// This vm's own scope when it has one, otherwise the integration's
		// list -- which is exactly what the vm can actually reach.
		scoped, err := h.q.ListVMIntegrationRepos(ctx, db.ListVMIntegrationReposParams{
			VMID: vmID, IntegrationID: r.ID,
		})
		if err != nil {
			log.Printf("could not read the repository scope of integration %s: %v", dto.ID, err)
			return echo.NewHTTPError(http.StatusInternalServerError, "could not list the integrations")
		}
		if len(scoped) > 0 {
			for _, x := range scoped {
				dto.Repos = append(dto.Repos, x.RepoOwner+"/"+x.RepoName)
			}
		} else if !r.AllRepos {
			covered, err := h.q.ListIntegrationRepos(ctx, r.ID)
			if err != nil {
				log.Printf("could not read the repositories of integration %s: %v", dto.ID, err)
				return echo.NewHTTPError(http.StatusInternalServerError, "could not list the integrations")
			}
			for _, x := range covered {
				dto.Repos = append(dto.Repos, x.RepoOwner+"/"+x.RepoName)
			}
		}
		dto.RepoCount = int64(len(dto.Repos))
		items = append(items, dto)
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
		log.Printf("could not attach integration %s to vm %s: %v", domainIDString(id), domainIDString(vmID), err)
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
		log.Printf("could not detach integration %s from vm %s: %v", domainIDString(id), domainIDString(vmID), err)
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
		log.Printf("could not read the github app: %v", err)
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

type repoRef struct{ owner, name string }

// parseRepoList keeps the spelling it was given -- github's own casing, when
// the list came from the picker -- but dedupes case-insensitively, since
// github treats two spellings of a name as one repository.
func parseRepoList(raw []string) ([]repoRef, error) {
	seen := map[string]bool{}
	var out []repoRef
	for _, s := range raw {
		parts := strings.Split(strings.TrimSpace(s), "/")
		if len(parts) != 2 || !githubNameRe.MatchString(parts[0]) || !githubNameRe.MatchString(parts[1]) {
			return nil, errors.New("each repository must be owner/name: " + s)
		}
		key := strings.ToLower(parts[0] + "/" + parts[1])
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, repoRef{owner: parts[0], name: parts[1]})
	}
	return out, nil
}

func notFoundOr(err error, notFound, other string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, notFound)
	}
	log.Printf("%s: %v", other, err)
	return echo.NewHTTPError(http.StatusInternalServerError, other)
}
