package main

import (
  "net/http"
  "strings"
  "time"

  "github.com/labstack/echo/v5"

  "control/internal/db"
)

// settingDTO is one row of the settings screen: the definition an admin needs
// to understand the setting, plus its current value and when it last changed.
//
// Value is always a string, whatever the kind -- the column is TEXT, and a
// single shape keeps the client from having to switch on the type to read it.
// For a secret it is always empty; IsSet is what the UI has instead.
type settingDTO struct {
  settingDef
  Value     string `json:"value"`
  IsSet     bool   `json:"is_set"`
  UpdatedAt string `json:"updated_at"` // "" = never written since the seed
}

func newSettingDTO(def settingDef, value string, updatedAt string) settingDTO {
  d := settingDTO{settingDef: def, Value: value, IsSet: value != "", UpdatedAt: updatedAt}
  if def.Kind == settingSecret {
    d.Value = ""
  }
  return d
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
    value, updatedAt := def.Default, ""
    if row, ok := stored[def.Key]; ok {
      value = row.Value
      if row.UpdatedAt.Valid {
        updatedAt = row.UpdatedAt.Time.Format(time.RFC3339)
      }
    }
    items = append(items, newSettingDTO(def, value, updatedAt))
  }
  return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// updateSettingReq takes the value as `any` because the kinds are stored in one
// TEXT column but written as their natural JSON type: a toggle sends true, a
// text field sends a string. Coerced against the declared kind below, so a
// client that sends the wrong one is told rather than silently storing "%!s".
type updateSettingReq struct {
  Value any `json:"value"`
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

  var value string
  switch def.Kind {
  case settingBool:
    b, ok := req.Value.(bool)
    if !ok {
      return echo.NewHTTPError(http.StatusBadRequest, "value must be true or false")
    }
    value = formatSettingBool(b)
  default:
    s, ok := req.Value.(string)
    if !ok {
      return echo.NewHTTPError(http.StatusBadRequest, "value must be a string")
    }
    // Trimmed because these are pasted: a trailing space in a URL or a version
    // is never meant, and both are used to build something that would fail
    // somewhere far less obvious than here.
    value = strings.TrimSpace(s)
  }
  if def.validate != nil {
    if err := def.validate(value); err != nil {
      return echo.NewHTTPError(http.StatusBadRequest, err.Error())
    }
  }

  params := db.UpsertSettingParams{Key: def.Key, Value: value}
  if uid, _ := c.Get("uid").(string); uid != "" {
    if pgID, err := parseUUID(uid); err == nil {
      params.UpdatedBy = pgID
    }
  }

  row, err := h.q.UpsertSetting(c.Request().Context(), params)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not save setting")
  }

  // A vector setting that only took effect on the next connect would mean an
  // admin changing the version sees nothing happen until every host happens to
  // reconnect. Pushed to everyone that is online instead; the rest pick it up
  // when they come back, because the connect path sends one unconditionally.
  if isVectorSetting(def.Key) {
    pushVectorConfigToAll(c.Request().Context(), h.q, h.hub)
  }

  updatedAt := ""
  if row.UpdatedAt.Valid {
    updatedAt = row.UpdatedAt.Time.Format(time.RFC3339)
  }
  return c.JSON(http.StatusOK, newSettingDTO(def, row.Value, updatedAt))
}
