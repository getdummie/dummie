package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type settingDTO struct {
	settingDef
	Value     string `json:"value"`
	IsSet     bool   `json:"is_set"`
	UpdatedAt string `json:"updated_at"`
}

func newSettingDTO(def settingDef, value string, updatedAt string) settingDTO {
	d := settingDTO{settingDef: def, Value: value, IsSet: value != "", UpdatedAt: updatedAt}
	if def.Kind == settingSecret {
		d.Value = ""
	}
	return d
}

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

type updateSettingReq struct {
	Value any `json:"value"`
}

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

	if isVectorSetting(def.Key) {
		pushVectorConfigToAll(c.Request().Context(), h.q, h.hub)
	}
	if def.Key == settingResolverUpstream {
		pushCoreDNSConfigToAll(c.Request().Context(), h.q, h.hub)
	}

	updatedAt := ""
	if row.UpdatedAt.Valid {
		updatedAt = row.UpdatedAt.Time.Format(time.RFC3339)
	}
	return c.JSON(http.StatusOK, newSettingDTO(def, row.Value, updatedAt))
}
