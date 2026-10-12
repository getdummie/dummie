package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const (
	auditSignin       = "auth.signin"
	auditSigninFailed = "auth.signin_failed"
	auditSignup       = "auth.signup"
	auditSignout      = "auth.signout"
	auditOIDCSignin   = "auth.oidc_signin"
	auditOIDCFailed   = "auth.oidc_failed"
)

const taskAuditPrune = "audit.prune"

const taskAuditPruneEvery = 24 * time.Hour

type auditEntry struct {
	actorID       pgtype.UUID
	// Overrides the username looked up from actorID, e.g. the identifier a failed sign-in tried.
	actorUsername string
	authMethod    string
	action        string
	params        map[string]string
	status        int
}

func recordAudit(c *echo.Context, q *db.Queries, e auditEntry) {
	params, err := json.Marshal(e.params)
	if err != nil || e.params == nil {
		params = []byte("{}")
	}
	if e.authMethod == "" {
		e.authMethod = "none"
	}
	r := c.Request()
	err = q.InsertAuditLog(context.WithoutCancel(r.Context()), db.InsertAuditLogParams{
		ActorID:       e.actorID,
		ActorUsername: pgtype.Text{String: e.actorUsername, Valid: e.actorUsername != ""},
		AuthMethod:    e.authMethod,
		Action:        e.action,
		Path:          r.URL.Path,
		Params:        params,
		Status:        int32(e.status),
		IP:            clientIP(c),
		UserAgent:     r.UserAgent(),
	})
	if err != nil {
		log.Printf("audit: could not record %s: %v", e.action, err)
	}
}

// POSTs that only read or are polled in a loop; recording them would bury real actions.
var auditSkipRoutes = map[string]bool{
	"/me/llm-keys/:provider/device/poll":    true,
	"/admin/llm-keys/:provider/device/poll": true,
	"/vms/:id/targets/resolve":              true,
}

// auditMW records every mutating request that reaches a handler. It must sit after
// the auth middleware so the caller is known.
func auditMW(q *db.Queries) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			err := next(c)
			switch c.Request().Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				return err
			}
			route := strings.TrimPrefix(c.Path(), "/api/v1")
			if auditSkipRoutes[route] {
				return err
			}

			actor, _ := callerID(c)
			method := "session"
			if a, _ := c.Get("auth").(string); a == "pat" {
				method = "pat"
			}
			params := map[string]string{}
			for _, pv := range c.PathValues() {
				params[pv.Name] = pv.Value
			}
			recordAudit(c, q, auditEntry{
				actorID:    actor,
				authMethod: method,
				action:     c.Request().Method + " " + route,
				params:     params,
				status:     responseStatus(c, err),
			})
			return err
		}
	}
}

func responseStatus(c *echo.Context, err error) int {
	if err != nil {
		var he *echo.HTTPError
		if errors.As(err, &he) {
			return he.Code
		}
		return http.StatusInternalServerError
	}
	if res, uerr := echo.UnwrapResponse(c.Response()); uerr == nil && res.Status != 0 {
		return res.Status
	}
	return http.StatusOK
}

type AuditHandler struct {
	q *db.Queries
}

type auditLogDTO struct {
	ID            int64             `json:"id"`
	CreatedAt     string            `json:"created_at"`
	ActorID       string            `json:"actor_id"`
	ActorUsername string            `json:"actor_username"`
	AuthMethod    string            `json:"auth_method"`
	Action        string            `json:"action"`
	Path          string            `json:"path"`
	Params        map[string]string `json:"params"`
	Status        int32             `json:"status"`
	IP            string            `json:"ip"`
	UserAgent     string            `json:"user_agent"`
}

func (h *AuditHandler) List(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	qs := c.Request().URL.Query()
	actor := strings.TrimSpace(qs.Get("actor"))
	action := strings.TrimSpace(qs.Get("action"))
	failed := qs.Get("failed") == "1" || strings.EqualFold(qs.Get("failed"), "true")

	total, err := h.q.CountAuditLogs(ctx, db.CountAuditLogsParams{Actor: actor, Action: action, FailedOnly: failed})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count audit entries")
	}
	rows, err := h.q.ListAuditLogs(ctx, db.ListAuditLogsParams{
		Actor: actor, Action: action, FailedOnly: failed, RowLimit: limit, RowOffset: offset,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list audit entries")
	}
	items := make([]auditLogDTO, 0, len(rows))
	for _, r := range rows {
		params := map[string]string{}
		_ = json.Unmarshal(r.Params, &params)
		actorID := ""
		if r.ActorID.Valid {
			actorID = uuid.UUID(r.ActorID.Bytes).String()
		}
		items = append(items, auditLogDTO{
			ID:            r.ID,
			CreatedAt:     r.CreatedAt.Time.Format(time.RFC3339),
			ActorID:       actorID,
			ActorUsername: r.ActorUsername,
			AuthMethod:    r.AuthMethod,
			Action:        r.Action,
			Path:          r.Path,
			Params:        params,
			Status:        r.Status,
			IP:            r.IP,
			UserAgent:     r.UserAgent,
		})
	}
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func handleAuditPrune(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	days := intSetting(ctx, r.q, settingAuditRetentionDays)
	n, err := r.q.DeleteAuditLogsBefore(ctx, int32(days))
	if err != nil {
		return taskRetry(taskExpireRetry, "could not prune the audit log: %v", err)
	}
	if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
		Kind:   taskAuditPrune,
		Reason: "prune audit log entries older than the retention setting",
		After:  taskAuditPruneEvery,
	}); err != nil {
		return taskRetry(taskExpireRetry, "pruned %d audit entries but could not schedule the next prune: %v", n, err)
	}
	return taskDone("pruned %d audit entries older than %d days", n, days)
}
