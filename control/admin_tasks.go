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

// The admin view of scheduled_tasks. The table is the audit log as well as the
// queue, so these routes are read-mostly: what is the control plane going to do,
// when, and what happened to the things it has already done.
//
// The two writes are deliberately narrow. Cancel withdraws work that should not
// happen; run-now brings work forward. Neither invents a task -- a task exists
// because somebody asked for a TTL, and an admin conjuring one out of the queue
// would be an action with no request behind it.

type scheduledTaskDTO struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`

	// The subject, as ids. The UI resolves the link from these; the human-readable
	// name lives in the payload, because it has to survive the subject's deletion.
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`

	Status string `json:"status"`

	// Payload is passed through as-is. It is the debugging half of this view: what
	// the task was scheduled with, in the shape the handler reads it.
	Payload json.RawMessage `json:"payload"`

	// Reason is why the task was scheduled; Detail is the latest word on it. Both
	// are sentences meant to be read as they are.
	Reason string `json:"reason"`
	Detail string `json:"detail"`

	RunAt string `json:"run_at"`
	// OverdueSeconds is how late a pending task is, and 0 for one that is not.
	// Computed here rather than in the browser because a client clock that is
	// wrong would make the fleet look broken, or hide that it is.
	OverdueSeconds int64 `json:"overdue_seconds"`

	Attempts    int32 `json:"attempts"`
	MaxAttempts int32 `json:"max_attempts"`

	// CreatedBy is "" for a task the control plane scheduled for itself.
	CreatedBy string `json:"created_by"`
	// LockedBy names the process currently holding the task, and is "" unless it
	// is running. Worth surfacing: a task stuck 'running' with a lease that never
	// moves is the signature of a process that died holding it.
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

// taskStatuses reads the status filter. Absent means the half of the table
// somebody is watching -- work that has not settled, plus the failures that need
// a human -- because a default of "everything" buries those under history the
// moment the fleet has any. 'all' is the way to ask for everything.
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
	// Every named status was unrecognised. Answering with "everything" would be a
	// typo silently widening the query, so it comes back as nothing instead.
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

	// The two numbers that decide whether this page is worth acting on, sent with
	// every read so the UI does not have to ask separately: how far behind the
	// runner is, and whether it is alive at all.
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

// CancelScheduledTask withdraws a task before it runs. What that means depends on
// the kind and the admin doing it is expected to know: cancelling a vm.expire
// makes a temporary sandbox permanent, and cancelling a vm_target.expire makes a
// temporary allowance permanent.
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
			// Either it does not exist or it is no longer pending. One answer for both,
			// and the message names the likely one -- a running task is the case an
			// operator hits, since the row was pending when the page was rendered.
			return echo.NewHTTPError(http.StatusConflict, "that task is not pending, so it cannot be cancelled")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not cancel the task")
	}
	return c.JSON(http.StatusOK, toScheduledTaskDTO(t))
}

// RunScheduledTaskNow brings a task forward, and is the way a failed one gets
// another go. Both are debugging actions: the ordinary path is that a task runs
// when it is due.
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
