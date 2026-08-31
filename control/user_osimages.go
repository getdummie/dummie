package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// A user's own images are capped two ways: how many live rows they may own, and
// how many builds of theirs may be in flight at once. The second one is what
// keeps one person from filling the task queue, since a build pulls and flattens
// a whole container image.
const (
	defaultUserOSImageLimit = 5
	userOSImageBuildLimit   = 1
)

func userOSImageLimit() int {
	return envInt("USER_OSIMAGE_LIMIT", defaultUserOSImageLimit)
}

type myOSImageList struct {
	Items []osImageDTO `json:"items"`
	Limit int          `json:"limit"`
}

// @Summary     List the OS images you added
// @Description Every image you built from a container reference, at any status: 'pending' and 'building' are still on their way, 'failed' carries the reason in status_detail, 'ready' is usable in POST /vms. The shared catalogue an admin curates is not in here -- that is GET /vms/osimages, which lists everything ready, yours included.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} myOSImageList
// @Failure     401 {object} apiError
// @Router      /vms/osimages/mine [get]
func (h *UserHandler) ListMyOSImages(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	rows, err := h.q.ListOSImagesByOwner(c.Request().Context(), db.ListOSImagesByOwnerParams{
		CreatedBy: owner,
		Limit:     maxArtifactChoices,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list your os images")
	}
	items := make([]osImageDTO, 0, len(rows))
	for _, o := range rows {
		items = append(items, toOSImageDTO(o))
	}
	return c.JSON(http.StatusOK, myOSImageList{Items: items, Limit: userOSImageLimit()})
}

// @Summary     Add an OS image from a container image
// @Description Name a container image and the server pulls it, flattens its layers into one rootfs and records what the image declared -- its user, entrypoint, command, environment and exposed ports -- so a VM built from it starts the same thing the container would have. The pull is anonymous and only from the registries this installation allows, so a private repository needs an admin.
// @Description
// @Description The response is the row at status 'pending'; the build runs in the background. Poll GET /vms/osimages/mine until it is 'ready', then pass its id as osimage_id to POST /vms. Every user sees a ready image, so the name has to be free across the whole installation.
// @Tags        vms
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body createOCIOSImageReq true "name and oci_ref are required"
// @Success     201 {object} osImageDTO "queued; the build has not run yet"
// @Failure     400 {object} apiError "no name, no reference, a reference that does not parse, or a registry this server does not pull from"
// @Failure     401 {object} apiError
// @Failure     403 {object} apiError "at your image limit, or a build of yours is already running"
// @Failure     409 {object} apiError "an os image with that name already exists"
// @Failure     503 {object} apiError "no blob store is configured on this installation"
// @Router      /vms/osimages [post]
func (h *UserHandler) CreateOSImage(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	var req createOCIOSImageReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	ctx := c.Request().Context()
	live, err := h.q.CountLiveOSImagesByOwner(ctx, owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count your os images")
	}
	if limit := userOSImageLimit(); int(live) >= limit {
		return echo.NewHTTPError(http.StatusForbidden, fmt.Sprintf(
			"you have %d os images of your own against a limit of %d; withdraw one first", live, limit))
	}
	building, err := h.q.CountBuildingOSImagesByOwner(ctx, owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count your builds")
	}
	if building >= userOSImageBuildLimit {
		return echo.NewHTTPError(http.StatusForbidden,
			"one of your images is still building; wait for it to finish")
	}

	o, err := queueOCIOSImage(ctx, h.q, h.blobs, req, owner)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, toOSImageDTO(o))
}

// @Summary     Withdraw an OS image you added
// @Description Only an image you added yourself. It leaves the catalogue for everyone, so VMs created from it keep running -- the rootfs is already on their host -- but nobody can create a new VM from it. A build still queued is cancelled.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "os image id"
// @Success     200 {object} osImageDTO
// @Failure     400 {object} apiError "invalid os image id"
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError "not yours, not found, or already withdrawn"
// @Router      /vms/osimages/{id} [delete]
func (h *UserHandler) DeleteOSImage(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid os image id")
	}
	ctx := c.Request().Context()
	o, err := h.q.SoftDeleteOSImageForOwner(ctx, db.SoftDeleteOSImageForOwnerParams{
		ID:        pgID,
		CreatedBy: owner,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "no os image of yours with that id")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not withdraw the os image")
	}
	cancelTasksForSubject(ctx, h.q, subjectOSImage, pgID, "the os image was withdrawn")
	return c.JSON(http.StatusOK, toOSImageDTO(o))
}
