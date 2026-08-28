package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type ProfileHandler struct {
	q *db.Queries
}

const maxNameLen = 100

// @Summary     Read your account
// @Description The same shape the admin section shows for a user. Nothing secret is in it: no password hash, and a public key is public.
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} adminUserDTO
// @Failure     401 {object} apiError
// @Router      /me [get]
func (h *ProfileHandler) GetMe(c *echo.Context) error {
	id, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	u, err := h.q.GetUserByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusUnauthorized, "your account no longer exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read your account")
	}
	return c.JSON(http.StatusOK, toAdminUserDTO(u))
}

type updateProfileReq struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	PublicKey string `json:"public_key"`
}

// @Summary     Update your account
// @Description Only the display name and the SSH public key are editable. Username, email, role and every allowance are an admin's to set. An empty public key is accepted and clears it, which blocks creating new VMs.
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body updateProfileReq true "the whole editable surface"
// @Success     200 {object} adminUserDTO
// @Failure     400 {object} apiError "a name over 100 characters, or a key that is not a valid public key"
// @Failure     401 {object} apiError
// @Failure     409 {object} apiError "that public key is already on another account"
// @Router      /me [put]
func (h *ProfileHandler) UpdateMe(c *echo.Context) error {
	id, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	var req updateProfileReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	if len(req.FirstName) > maxNameLen || len(req.LastName) > maxNameLen {
		return echo.NewHTTPError(http.StatusBadRequest, "a name cannot be longer than 100 characters")
	}
	key, err := normalizePublicKey(req.PublicKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	u, err := h.q.UpdateUserProfile(c.Request().Context(), db.UpdateUserProfileParams{
		ID:        id,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		PublicKey: key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusUnauthorized, "your account no longer exists")
		}
		if isDuplicatePublicKey(err) {
			return echo.NewHTTPError(http.StatusConflict, "that public key is already on another account; a key identifies one user")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save your profile")
	}
	return c.JSON(http.StatusOK, toAdminUserDTO(u))
}
