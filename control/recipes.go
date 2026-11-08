package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"control/internal/db"
	"control/internal/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"
)

const (
	maxRecipeName        = 64
	maxRecipeDescription = 500
)

type recipeIntegration struct {
	ID    string   `json:"id"`
	Repos []string `json:"repos,omitempty"`
}

// recipeConfig is the create form with every field optional: whatever is left
// out falls back to the form's own default at launch.
type recipeConfig struct {
	OSImageID    string              `json:"osimage_id,omitempty"`
	KernelID     string              `json:"kernel_id,omitempty"`
	CPUs         int32               `json:"cpus,omitempty"`
	MemoryMiB    int32               `json:"memory_mib,omitempty"`
	DiskSize     string              `json:"disk_size,omitempty"`
	DefaultPort  int32               `json:"default_port,omitempty"`
	PublicPorts  []int32             `json:"public_ports,omitempty"`
	TTLSeconds   int64               `json:"ttl_seconds,omitempty"`
	InitScript   string              `json:"init_script,omitempty"`
	Targets      []createTargetReq   `json:"targets,omitempty"`
	Integrations []recipeIntegration `json:"integrations,omitempty"`
}

type recipeDTO struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Config      recipeConfig `json:"config"`
	CreatedAt   string       `json:"created_at"`
	UpdatedAt   string       `json:"updated_at"`
}

type recipeReq struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Config      recipeConfig `json:"config"`
}

type saveVMRecipeReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func toRecipeDTO(r db.Recipe) recipeDTO {
	var cfg recipeConfig
	_ = json.Unmarshal(r.Config, &cfg)
	return recipeDTO{
		ID:          uuid.UUID(r.ID.Bytes).String(),
		Name:        r.Name,
		Description: r.Description,
		Config:      cfg,
		CreatedAt:   r.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:   r.UpdatedAt.Time.Format(time.RFC3339),
	}
}

func normalizeRecipeMeta(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || len(name) > maxRecipeName {
		return "", "", fmt.Errorf("a recipe needs a name of at most %d characters", maxRecipeName)
	}
	if len(description) > maxRecipeDescription {
		return "", "", fmt.Errorf("the description is over %d characters", maxRecipeDescription)
	}
	return name, description, nil
}

// validateRecipeConfig checks shape only. Ownership, quota and whether the
// image or kernel still exist are left to the create, since they can change.
func validateRecipeConfig(cfg *recipeConfig) error {
	cfg.OSImageID = strings.TrimSpace(cfg.OSImageID)
	cfg.KernelID = strings.TrimSpace(cfg.KernelID)
	cfg.DiskSize = strings.TrimSpace(cfg.DiskSize)
	if strings.TrimSpace(cfg.InitScript) == "" {
		cfg.InitScript = ""
	}
	for _, id := range []string{cfg.OSImageID, cfg.KernelID} {
		if id == "" {
			continue
		}
		if _, err := parseUUID(id); err != nil {
			return fmt.Errorf("%q is not a valid id", id)
		}
	}
	if cfg.CPUs < 0 {
		return errors.New("cpus must be at least 1")
	}
	if cfg.MemoryMiB != 0 && cfg.MemoryMiB < 64 {
		return errors.New("memory must be at least 64 MiB")
	}
	if cfg.DiskSize != "" {
		if _, err := parseSizeMiB(cfg.DiskSize); err != nil {
			return err
		}
	}
	if cfg.DefaultPort != 0 || len(cfg.PublicPorts) > 0 {
		if _, _, err := normalizePorts(cfg.DefaultPort, cfg.PublicPorts); err != nil {
			return err
		}
	}
	if _, err := parseTTLSeconds(cfg.TTLSeconds); err != nil {
		return err
	}
	if len(cfg.InitScript) > maxInitScript {
		return fmt.Errorf("the init script is over %d KiB", maxInitScript>>10)
	}
	if len(cfg.Targets) > maxCreateTargets {
		return fmt.Errorf("a recipe can hold at most %d destinations", maxCreateTargets)
	}
	for _, t := range cfg.Targets {
		if _, err := normalizeTarget(t); err != nil {
			return err
		}
	}
	for _, in := range cfg.Integrations {
		if _, err := parseUUID(in.ID); err != nil {
			return fmt.Errorf("%q is not a valid integration id", in.ID)
		}
		if _, err := parseRepoList(in.Repos); err != nil {
			return err
		}
	}
	return nil
}

// @Summary     List your recipes
// @Tags        recipes
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string][]recipeDTO
// @Router      /recipes [get]
func (h *UserHandler) ListRecipes(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	rows, err := h.q.ListRecipesByOwner(c.Request().Context(), owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list your recipes")
	}
	items := make([]recipeDTO, 0, len(rows))
	for _, r := range rows {
		items = append(items, toRecipeDTO(r))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// @Summary     Read one of your recipes
// @Tags        recipes
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "recipe id" format(uuid)
// @Success     200 {object} recipeDTO
// @Failure     404 {object} apiError
// @Router      /recipes/{id} [get]
func (h *UserHandler) GetRecipe(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid recipe id")
	}
	r, err := h.q.GetRecipeForOwner(c.Request().Context(), db.GetRecipeForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such recipe", "could not read the recipe")
	}
	return c.JSON(http.StatusOK, toRecipeDTO(r))
}

// @Summary     Save a recipe
// @Description Everything in config is optional; a field left out falls back to the create form's default when the recipe is launched. Ids are not checked here -- an image or integration can go away after the recipe is saved, so the create that launches it is where they are checked.
// @Tags        recipes
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body recipeReq true "name is required"
// @Success     201 {object} recipeDTO
// @Failure     400 {object} apiError
// @Failure     409 {object} apiError "you already have a recipe with that name"
// @Router      /recipes [post]
func (h *UserHandler) CreateRecipe(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	var req recipeReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	return h.insertRecipe(c, owner, req)
}

func (h *UserHandler) insertRecipe(c *echo.Context, owner pgtype.UUID, req recipeReq) error {
	name, desc, err := normalizeRecipeMeta(req.Name, req.Description)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := validateRecipeConfig(&req.Config); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	raw, err := json.Marshal(req.Config)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the recipe")
	}
	r, err := h.q.CreateRecipe(c.Request().Context(), db.CreateRecipeParams{
		OwnerID: owner, Name: name, Description: desc, Config: raw,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "you already have a recipe with that name")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the recipe")
	}
	return c.JSON(http.StatusCreated, toRecipeDTO(r))
}

// @Summary     Replace one of your recipes
// @Tags        recipes
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "recipe id" format(uuid)
// @Param       body body recipeReq true "name is required"
// @Success     200 {object} recipeDTO
// @Failure     400 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "you already have a recipe with that name"
// @Router      /recipes/{id} [put]
func (h *UserHandler) UpdateRecipe(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid recipe id")
	}
	var req recipeReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	name, desc, err := normalizeRecipeMeta(req.Name, req.Description)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := validateRecipeConfig(&req.Config); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	raw, err := json.Marshal(req.Config)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the recipe")
	}
	r, err := h.q.UpdateRecipeForOwner(c.Request().Context(), db.UpdateRecipeForOwnerParams{
		ID: id, OwnerID: owner, Name: name, Description: desc, Config: raw,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "you already have a recipe with that name")
		}
		return notFoundOr(err, "no such recipe", "could not save the recipe")
	}
	return c.JSON(http.StatusOK, toRecipeDTO(r))
}

// @Summary     Delete one of your recipes
// @Description VMs launched from it are not affected.
// @Tags        recipes
// @Security    BearerAuth
// @Param       id path string true "recipe id" format(uuid)
// @Success     204
// @Failure     404 {object} apiError
// @Router      /recipes/{id} [delete]
func (h *UserHandler) DeleteRecipe(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid recipe id")
	}
	n, err := h.q.DeleteRecipeForOwner(c.Request().Context(), db.DeleteRecipeForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the recipe")
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "no such recipe")
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Save one of your VMs as a recipe
// @Description Takes the VM's image, kernel, sizes, ports, init script, integrations and its permanent allowed destinations. Temporary destinations and "allow everything" are left out. A VM created before recipes existed has no record of its OS image, so that field is left empty.
// @Tags        recipes
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "vm id" format(uuid)
// @Param       body body saveVMRecipeReq true "name is required"
// @Success     201 {object} recipeDTO
// @Failure     400 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "you already have a recipe with that name"
// @Router      /vms/{id}/recipe [post]
func (h *UserHandler) SaveVMAsRecipe(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	var req saveVMRecipeReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	cfg, err := h.recipeFromVM(c.Request().Context(), vm)
	if err != nil {
		log.Printf("could not read vm %s for a recipe: %v", uuid.UUID(vm.ID.Bytes), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the vm")
	}
	return h.insertRecipe(c, vm.CreatedBy, recipeReq{Name: req.Name, Description: req.Description, Config: cfg})
}

func (h *UserHandler) recipeFromVM(ctx context.Context, vm db.Vm) (recipeConfig, error) {
	var spec proto.VMSpec
	_ = json.Unmarshal(vm.Spec, &spec)
	cfg := recipeConfig{
		CPUs:        vm.CPUs,
		MemoryMiB:   vm.MemoryMiB,
		DiskSize:    spec.DiskSize,
		DefaultPort: vm.DefaultPort,
		PublicPorts: vm.PublicPorts,
		InitScript:  spec.InitScript,
	}
	if vm.OSImageID.Valid {
		cfg.OSImageID = uuid.UUID(vm.OSImageID.Bytes).String()
	}
	if vm.KernelID.Valid {
		cfg.KernelID = uuid.UUID(vm.KernelID.Bytes).String()
	}

	targets, err := h.q.ListVMNetworkTargets(ctx, vm.ID)
	if err != nil {
		return cfg, err
	}
	ids := make([]pgtype.UUID, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	temporary := liveTaskDeadlines(ctx, h.q, subjectVMTarget, ids)
	for _, t := range targets {
		if _, ok := temporary[uuid.UUID(t.ID.Bytes).String()]; ok {
			continue
		}
		if t.Kind == "ip" && t.Destination == targetEverywhere && t.Transport == "any" && t.Ports == "" {
			continue
		}
		cfg.Targets = append(cfg.Targets, createTargetReq{
			Kind: t.Kind, Destination: t.Destination, Transport: t.Transport, Ports: t.Ports, Note: t.Note,
		})
	}

	integs, err := h.q.ListVMIntegrationsForOwner(ctx, db.ListVMIntegrationsForOwnerParams{
		VMID: vm.ID, OwnerID: vm.CreatedBy,
	})
	if err != nil {
		return cfg, err
	}
	for _, in := range integs {
		scoped, err := h.q.ListVMIntegrationRepos(ctx, db.ListVMIntegrationReposParams{
			VMID: vm.ID, IntegrationID: in.ID,
		})
		if err != nil {
			return cfg, err
		}
		ri := recipeIntegration{ID: uuid.UUID(in.ID.Bytes).String()}
		for _, s := range scoped {
			ri.Repos = append(ri.Repos, s.RepoOwner+"/"+s.RepoName)
		}
		cfg.Integrations = append(cfg.Integrations, ri)
	}
	return cfg, nil
}

type createAttachment struct {
	id    pgtype.UUID
	repos []repoRef
}

func (a createAttachment) apply(ctx context.Context, q *db.Queries, vmID pgtype.UUID) error {
	if err := q.AttachIntegrationToVM(ctx, db.AttachIntegrationToVMParams{VMID: vmID, IntegrationID: a.id}); err != nil {
		return err
	}
	for _, r := range a.repos {
		if err := q.InsertVMIntegrationRepo(ctx, db.InsertVMIntegrationRepoParams{
			VMID: vmID, IntegrationID: a.id, RepoOwner: r.owner, RepoName: r.name,
		}); err != nil {
			return err
		}
	}
	return nil
}

// integrationsForCreate checks the attachments a create asks for exactly as
// the attach and scope endpoints would, before anything is written.
func (h *UserHandler) integrationsForCreate(ctx context.Context, owner pgtype.UUID, req []recipeIntegration) ([]createAttachment, error) {
	seen := map[string]bool{}
	var out []createAttachment
	for _, in := range req {
		id, err := parseUUID(strings.TrimSpace(in.ID))
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid integration id")
		}
		key := uuid.UUID(id.Bytes).String()
		if seen[key] {
			continue
		}
		seen[key] = true
		row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
		if err != nil {
			return nil, notFoundOr(err, "an integration in this request no longer exists", "could not read the integration")
		}
		if row.Kind == integrationKindGitHub && !row.InstallationPk.Valid {
			return nil, echo.NewHTTPError(http.StatusConflict, "connect a github account to "+row.Name+" first")
		}
		repos, err := parseRepoList(in.Repos)
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if len(repos) > 0 {
			if err := githubOnly(row.Kind); err != nil {
				return nil, err
			}
			if err := checkReposCovered(ctx, h.q, row, repos); err != nil {
				return nil, err
			}
		}
		out = append(out, createAttachment{id: id, repos: repos})
	}
	return out, nil
}
