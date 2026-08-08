package main

import (
  "errors"
  "net/http"
  "strings"

  "github.com/jackc/pgx/v5"
  "github.com/labstack/echo/v5"

  "control/internal/db"
)

// ProfileHandler serves the two /me routes: a signed-in account reading and
// editing its own row. There is no id in either path -- the only row reachable
// here is the one the JWT names, so "may I edit this user" is not a question
// these handlers can be asked.
type ProfileHandler struct {
  q *db.Queries
}

// maxNameLen bounds the two free-text fields. A display name longer than this is
// not a name, and the column is unbounded TEXT.
const maxNameLen = 100

func (h *ProfileHandler) GetMe(c *echo.Context) error {
  id, err := callerID(c)
  if err != nil {
    return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }
  u, err := h.q.GetUserByID(c.Request().Context(), id)
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      // The token names an account that no longer exists -- deleted mid-session.
      return echo.NewHTTPError(http.StatusUnauthorized, "your account no longer exists")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read your account")
  }
  return c.JSON(http.StatusOK, toAdminUserDTO(u))
}

// updateProfileReq is the whole editable surface of an account: the display name
// and the public key. Username, email, role and every allowance are deliberately
// absent -- a field a user could send is a field a user could change, and those
// are an admin's to set.
type updateProfileReq struct {
  FirstName string `json:"first_name"`
  LastName  string `json:"last_name"`
  PublicKey string `json:"public_key"`
}

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
  // Empty is allowed: removing your key is a real thing to want, and the VM
  // create path is where the absence is answered for.
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
    return echo.NewHTTPError(http.StatusInternalServerError, "could not save your profile")
  }
  return c.JSON(http.StatusOK, toAdminUserDTO(u))
}
