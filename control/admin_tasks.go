package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type scheduledTaskDTO struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`

	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`

	Status string `json:"status"`

	Payload json.RawMessage `json:"payload"`

	Reason string `json:"reason"`
	Detail string `json:"detail"`

	RunAt string `json:"run_at"`
	OverdueSeconds int64 `json:"overdue_seconds"`

	Attempts    int32 `json:"attempts"`
	MaxAttempts int32 `json:"max_attempts"`

	CreatedBy string `json:"created_by"`
	LockedBy string `json:"locked_by"`

	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	FinishedAt string `json:"finished_at"`
}

func toScheduledTaskDTO(t db.ScheduledTask) scheduledTaskDTO {
	d := scheduledTaskDTO{
		ID:          uuid.UUID(t.ID.Bytes).String(),
		Kind:        t.Kind,
		SubjectKind: t.SubjectKind,
		Status:      t.Status,
		Payload:     json.RawMessage(t.Payload),
		Reason:      t.Reason,
		Detail:      t.Detail,
		Attempts:    t.Attempts,
		MaxAttempts: t.MaxAttempts,
		LockedBy:    t.LockedBy,
		RunAt:       t.RunAt.Time.Format(time.RFC3339),
		CreatedAt:   t.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.Time.Format(time.RFC3339),
	}
	if len(d.Payload) == 0 {
		d.Payload = json.RawMessage("{}")
	}
	if t.SubjectID.Valid {
		d.SubjectID = uuid.UUID(t.SubjectID.Bytes).String()
	}
	if t.CreatedBy.Valid {
		d.CreatedBy = uuid.UUID(t.CreatedBy.Bytes).String()
	}
	if t.FinishedAt.Valid {
		d.FinishedAt = t.FinishedAt.Time.Format(time.RFC3339)
	}
	if t.Status == "pending" {
		if late := time.Since(t.RunAt.Time); late > taskOverdueGrace {
			d.OverdueSeconds = int64(late.Seconds())
		}
	}
	return d
}

func taskStatuses(c *echo.Context) []string {
	raw := strings.TrimSpace(c.Request().URL.Query().Get("status"))
	if raw == "" {
		return []string{"pending", "running", "failed"}
	}
	if raw == "all" {
		return []string{}
	}
	out := []string{}
	for _, s := range strings.Split(raw, ",") {
		switch s = strings.TrimSpace(s); s {
		case "pending", "running", "done", "failed", "cancelled":
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

func (h *AdminHandler) ListScheduledTasks(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	statuses := taskStatuses(c)
	kind := strings.TrimSpace(c.Request().URL.Query().Get("kind"))

	total, err := h.q.CountScheduledTasks(ctx, db.CountScheduledTasksParams{
		Statuses: statuses, Kind: kind,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count scheduled tasks")
	}
	rows, err := h.q.ListScheduledTasks(ctx, db.ListScheduledTasksParams{
		Statuses: statuses, Kind: kind, Lim: limit, Off: offset,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list scheduled tasks")
	}
	items := make([]scheduledTaskDTO, 0, len(rows))
	for _, t := range rows {
		items = append(items, toScheduledTaskDTO(t))
	}

	env := pageEnvelope(items, total, limit, offset)
	overdue, err := h.tasks.overdue(ctx)
	if err == nil {
		env["overdue"] = overdue
	}
	if last := h.tasks.lastTickAt(); !last.IsZero() {
		env["last_tick_at"] = last.Format(time.RFC3339)
	}
	return c.JSON(http.StatusOK, env)
}

func (h *AdminHandler) GetScheduledTask(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	t, err := h.q.GetScheduledTask(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such task")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read task")
	}
	return c.JSON(http.StatusOK, toScheduledTaskDTO(t))
}

func (h *AdminHandler) CancelScheduledTask(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	t, err := h.q.CancelScheduledTask(c.Request().Context(), db.CancelScheduledTaskParams{
		ID:     id,
		Detail: "cancelled by an admin",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusConflict, "that task is not pending, so it cannot be cancelled")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not cancel the task")
	}
	return c.JSON(http.StatusOK, toScheduledTaskDTO(t))
}

func (h *AdminHandler) RunScheduledTaskNow(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}
	t, err := h.q.RunScheduledTaskNow(c.Request().Context(), db.RunScheduledTaskNowParams{
		ID:     id,
		Detail: "brought forward by an admin",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusConflict,
				"only a pending or failed task can be run; this one is already settled")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not reschedule the task")
	}
	return c.JSON(http.StatusAccepted, toScheduledTaskDTO(t))
}
