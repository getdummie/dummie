package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"control/internal/db"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"
)

const (
	maxWorkspaceLayoutName = 64
	workspaceCols          = 24
	workspaceRows          = 24
	defaultWorkspaceID     = "default"
)

var workspacePanels = map[string]bool{
	"chat": true, "console": true, "files": true, "transfer": true,
	"network": true, "diff": true, "sessions": true, "desktop": true,
}

type workspaceItem struct {
	Panel string `json:"panel"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
}

// workspaceGrid places panels on a fixed 24x24 grid that fills the page.
type workspaceGrid struct {
	Items []workspaceItem `json:"items"`
}

type workspaceLayoutDTO struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Builtin   bool          `json:"builtin"`
	Layout    workspaceGrid `json:"layout"`
	CreatedAt string        `json:"created_at,omitempty"`
	UpdatedAt string        `json:"updated_at,omitempty"`
}

// defaultWorkspaceLayout is the same for everyone and cannot be edited. A VM
// without a layout of its own opens with it.
var defaultWorkspaceLayout = workspaceLayoutDTO{
	ID:      defaultWorkspaceID,
	Name:    "default",
	Builtin: true,
	Layout: workspaceGrid{Items: []workspaceItem{
		{Panel: "sessions", X: 0, Y: 0, W: 4, H: 24},
		{Panel: "chat", X: 4, Y: 0, W: 12, H: 24},
		{Panel: "diff", X: 16, Y: 0, W: 8, H: 24},
	}},
}

type workspaceLayoutReq struct {
	Name   string        `json:"name"`
	Layout workspaceGrid `json:"layout"`
}

type vmWorkspaceReq struct {
	LayoutID string `json:"layout_id"`
}

func toWorkspaceLayoutDTO(w db.WorkspaceLayout) workspaceLayoutDTO {
	var grid workspaceGrid
	_ = json.Unmarshal(w.Layout, &grid)
	if grid.Items == nil {
		grid.Items = []workspaceItem{}
	}
	return workspaceLayoutDTO{
		ID:        uuid.UUID(w.ID.Bytes).String(),
		Name:      w.Name,
		Layout:    grid,
		CreatedAt: w.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt: w.UpdatedAt.Time.Format(time.RFC3339),
	}
}

func validateWorkspaceGrid(g workspaceGrid) error {
	var taken [workspaceRows][workspaceCols]bool
	seen := map[string]bool{}
	for _, it := range g.Items {
		if !workspacePanels[it.Panel] {
			return fmt.Errorf("%q is not a workspace panel", it.Panel)
		}
		if seen[it.Panel] {
			return fmt.Errorf("the %s panel is in the layout twice", it.Panel)
		}
		seen[it.Panel] = true
		if it.X < 0 || it.Y < 0 || it.W < 1 || it.H < 1 || it.X+it.W > workspaceCols || it.Y+it.H > workspaceRows {
			return fmt.Errorf("the %s panel does not fit the %dx%d grid", it.Panel, workspaceCols, workspaceRows)
		}
		for y := it.Y; y < it.Y+it.H; y++ {
			for x := it.X; x < it.X+it.W; x++ {
				if taken[y][x] {
					return fmt.Errorf("the %s panel overlaps another panel", it.Panel)
				}
				taken[y][x] = true
			}
		}
	}
	return nil
}

func normalizeWorkspaceLayout(req *workspaceLayoutReq) ([]byte, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > maxWorkspaceLayoutName {
		return nil, fmt.Errorf("a layout needs a name of at most %d characters", maxWorkspaceLayoutName)
	}
	if strings.EqualFold(req.Name, defaultWorkspaceLayout.Name) {
		return nil, fmt.Errorf("%q is the name of the built-in layout", defaultWorkspaceLayout.Name)
	}
	if req.Layout.Items == nil {
		req.Layout.Items = []workspaceItem{}
	}
	if err := validateWorkspaceGrid(req.Layout); err != nil {
		return nil, err
	}
	return json.Marshal(req.Layout)
}

func parseLayoutID(raw string) (pgtype.UUID, error) {
	if raw == defaultWorkspaceID {
		return pgtype.UUID{}, echo.NewHTTPError(http.StatusForbidden, "the default layout cannot be changed; duplicate it instead")
	}
	id, err := parseUUID(raw)
	if err != nil {
		return pgtype.UUID{}, echo.NewHTTPError(http.StatusBadRequest, "invalid layout id")
	}
	return id, nil
}

// @Summary     List workspace layouts
// @Description The built-in default layout comes first, then yours.
// @Tags        workspaces
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string][]workspaceLayoutDTO
// @Router      /workspace-layouts [get]
func (h *UserHandler) ListWorkspaceLayouts(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	rows, err := h.q.ListWorkspaceLayoutsByOwner(c.Request().Context(), owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list your layouts")
	}
	items := make([]workspaceLayoutDTO, 0, len(rows)+1)
	items = append(items, defaultWorkspaceLayout)
	for _, w := range rows {
		items = append(items, toWorkspaceLayoutDTO(w))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// @Summary     Save a workspace layout
// @Description Panels sit on a 24x24 grid, each panel at most once and none overlapping.
// @Tags        workspaces
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body workspaceLayoutReq true "name is required"
// @Success     201 {object} workspaceLayoutDTO
// @Failure     400 {object} apiError
// @Failure     409 {object} apiError "you already have a layout with that name"
// @Router      /workspace-layouts [post]
func (h *UserHandler) CreateWorkspaceLayout(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	var req workspaceLayoutReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	raw, err := normalizeWorkspaceLayout(&req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	w, err := h.q.CreateWorkspaceLayout(c.Request().Context(), db.CreateWorkspaceLayoutParams{OwnerID: owner, Name: req.Name, Layout: raw})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "you already have a layout with that name")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the layout")
	}
	return c.JSON(http.StatusCreated, toWorkspaceLayoutDTO(w))
}

// @Summary     Replace one of your workspace layouts
// @Description Every VM using this layout sees the change. The built-in default layout cannot be changed.
// @Tags        workspaces
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "layout id" format(uuid)
// @Param       body body workspaceLayoutReq true "name is required"
// @Success     200 {object} workspaceLayoutDTO
// @Failure     400 {object} apiError
// @Failure     403 {object} apiError "the default layout cannot be changed"
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "you already have a layout with that name"
// @Router      /workspace-layouts/{id} [put]
func (h *UserHandler) UpdateWorkspaceLayout(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	id, err := parseLayoutID(c.Param("id"))
	if err != nil {
		return err
	}
	var req workspaceLayoutReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	raw, err := normalizeWorkspaceLayout(&req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	w, err := h.q.UpdateWorkspaceLayoutForOwner(c.Request().Context(), db.UpdateWorkspaceLayoutForOwnerParams{
		ID: id, OwnerID: owner, Name: req.Name, Layout: raw,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "you already have a layout with that name")
		}
		return notFoundOr(err, "no such layout", "could not save the layout")
	}
	return c.JSON(http.StatusOK, toWorkspaceLayoutDTO(w))
}

// @Summary     Delete one of your workspace layouts
// @Description VMs that used it go back to the default layout.
// @Tags        workspaces
// @Security    BearerAuth
// @Param       id path string true "layout id" format(uuid)
// @Success     204
// @Failure     403 {object} apiError "the default layout cannot be changed"
// @Failure     404 {object} apiError
// @Router      /workspace-layouts/{id} [delete]
func (h *UserHandler) DeleteWorkspaceLayout(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	id, err := parseLayoutID(c.Param("id"))
	if err != nil {
		return err
	}
	n, err := h.q.DeleteWorkspaceLayoutForOwner(c.Request().Context(), db.DeleteWorkspaceLayoutForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the layout")
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "no such layout")
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Read the workspace layout a VM opens with
// @Description The layout picked for this VM, or the built-in default layout when none was picked.
// @Tags        workspaces
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} workspaceLayoutDTO
// @Failure     404 {object} apiError
// @Router      /vms/{id}/workspace [get]
func (h *UserHandler) GetVMWorkspace(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	w, err := h.q.GetVMWorkspaceLayout(c.Request().Context(), db.GetVMWorkspaceLayoutParams{VMID: vm.ID, OwnerID: vm.CreatedBy})
	if errors.Is(err, pgx.ErrNoRows) {
		return c.JSON(http.StatusOK, defaultWorkspaceLayout)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the workspace layout")
	}
	return c.JSON(http.StatusOK, toWorkspaceLayoutDTO(w))
}

// @Summary     Pick the workspace layout a VM opens with
// @Description Pass "default" as the layout id to go back to the built-in default layout.
// @Tags        workspaces
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "vm id" format(uuid)
// @Param       body body vmWorkspaceReq true "one of your layouts, or default"
// @Success     200 {object} workspaceLayoutDTO
// @Failure     400 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/workspace [put]
func (h *UserHandler) SetVMWorkspace(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	var req vmWorkspaceReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	if req.LayoutID == defaultWorkspaceID {
		if err := h.q.ClearVMWorkspaceLayout(ctx, vm.ID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not pick the layout")
		}
		return c.JSON(http.StatusOK, defaultWorkspaceLayout)
	}
	layoutID, err := parseUUID(req.LayoutID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid layout id")
	}
	w, err := h.q.GetWorkspaceLayoutForOwner(ctx, db.GetWorkspaceLayoutForOwnerParams{ID: layoutID, OwnerID: vm.CreatedBy})
	if err != nil {
		return notFoundOr(err, "no such layout", "could not pick the layout")
	}
	if err := h.q.SetVMWorkspaceLayout(ctx, db.SetVMWorkspaceLayoutParams{VMID: vm.ID, LayoutID: w.ID}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not pick the layout")
	}
	return c.JSON(http.StatusOK, toWorkspaceLayoutDTO(w))
}
