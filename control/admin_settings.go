package main

import (
  "net/http"
  "time"

  "github.com/labstack/echo/v5"

  "control/internal/db"
)

// settingDTO is one row of the settings screen: the definition an admin needs
// to understand the toggle, plus its current value and who last changed it.
type settingDTO struct {
  settingDef
  Value     bool   `json:"value"`
  UpdatedAt string `json:"updated_at"` // "" = never written since the seed
}

// ListSettings returns every known setting, whether or not it has a row. The
// list is driven by settingDefs rather than by the table so a setting added in
// code shows up (at its default) before anyone has written it.
func (h *AdminHandler) ListSettings(c *echo.Context) error {
  rows, err := h.q.ListSettings(c.Request().Context())
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not load settings")
  }
  stored := make(map[string]db.Setting, len(rows))
  for _, r := range rows {
    stored[r.Key] = r
  }

  items := make([]settingDTO, 0, len(settingDefs))
  for _, def := range settingDefs {
    d := settingDTO{settingDef: def, Value: def.Default}
    if row, ok := stored[def.Key]; ok {
      d.Value = parseSettingBool(row.Value)
      if row.UpdatedAt.Valid {
        d.UpdatedAt = row.UpdatedAt.Time.Format(time.RFC3339)
      }
    }
    items = append(items, d)
  }
  return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type updateSettingReq struct {
  Value bool `json:"value"`
}

// UpdateSetting writes one setting. The key must be one the server declares:
// an unknown key is a 404, not a new row, so this endpoint can never be used to
// write arbitrary keys into the table.
func (h *AdminHandler) UpdateSetting(c *echo.Context) error {
  key := c.Param("key")
  def, ok := settingDefByKey(key)
  if !ok {
    return echo.NewHTTPError(http.StatusNotFound, "unknown setting")
  }

  var req updateSettingReq
  if err := c.Bind(&req); err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
  }

  params := db.UpsertSettingParams{Key: def.Key, Value: formatSettingBool(req.Value)}
  if uid, _ := c.Get("uid").(string); uid != "" {
    if pgID, err := parseUUID(uid); err == nil {
      params.UpdatedBy = pgID
    }
  }

  row, err := h.q.UpsertSetting(c.Request().Context(), params)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not save setting")
  }

  d := settingDTO{settingDef: def, Value: parseSettingBool(row.Value)}
  if row.UpdatedAt.Valid {
    d.UpdatedAt = row.UpdatedAt.Time.Format(time.RFC3339)
  }
  return c.JSON(http.StatusOK, d)
}
