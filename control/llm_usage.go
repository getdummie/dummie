package main

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// LLMUsageHandler reports global-key usage to admins and both key sources to
// the owner. Personal usage is marked in the metered provider name so it also
// works with existing Vector and ClickHouse installations.
type LLMUsageHandler struct {
	q      *db.Queries
	ch     driver.Conn
	prices *llmPriceBook
}

type usageTotals struct {
	Requests   uint64  `json:"requests"`
	Input      uint64  `json:"input_tokens"`
	Output     uint64  `json:"output_tokens"`
	CacheRead  uint64  `json:"cache_read_tokens"`
	CacheWrite uint64  `json:"cache_write_tokens"`
	Cost       float64 `json:"cost_usd"`
	// Unpriced means some of this usage is on a model with no known price, so
	// Cost covers only the rest.
	Unpriced bool `json:"unpriced"`
}

func (t *usageTotals) add(o usageTotals) {
	t.Requests += o.Requests
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Cost += o.Cost
	t.Unpriced = t.Unpriced || o.Unpriced
}

type usageByModel struct {
	Provider string      `json:"provider"`
	Model    string      `json:"model"`
	Source   usageSource `json:"source"`
	usageTotals
}

type usageByVM struct {
	VMID   string `json:"vm_id"`
	VMName string `json:"vm_name"`
	usageTotals
}

type usageByDay struct {
	Date string `json:"date"`
	usageTotals
}

type usageReport struct {
	Month     string         `json:"month"`
	Source    usageSource    `json:"source"`
	Available bool           `json:"available"`
	Totals    usageTotals    `json:"totals"`
	ByModel   []usageByModel `json:"by_model"`
	ByVM      []usageByVM    `json:"by_vm"`
	ByDay     []usageByDay   `json:"by_day"`
}

type usageByUser struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	usageTotals
}

// usageMonth is the calendar month in UTC; an empty param is the current one.
func usageMonth(raw string) (time.Time, error) {
	if raw == "" {
		now := time.Now().UTC()
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), nil
	}
	return time.Parse("2006-01", raw)
}

const usageRowsQuery = `
SELECT user_id, vm_id, toString(toDate(timestamp)) AS day, provider, model,
       count(), sum(input_tokens), sum(output_tokens), sum(cache_read_tokens), sum(cache_write_tokens)
FROM llm_usage
WHERE timestamp >= ? AND timestamp < ? AND (? OR user_id = toUUID(?))
  AND (? OR endsWith(provider, '@personal') = ?)
GROUP BY user_id, vm_id, day, provider, model`

type usageSource string

const (
	usageGlobal   usageSource = "global"
	usagePersonal usageSource = "personal"
	usageAll      usageSource = "all"
)

func parseUsageSource(raw string) (usageSource, bool) {
	switch usageSource(raw) {
	case "", usageGlobal:
		return usageGlobal, true
	case usagePersonal:
		return usagePersonal, true
	case usageAll:
		return usageAll, true
	default:
		return "", false
	}
}

type usageRow struct {
	user            uuid.UUID
	vm              uuid.UUID
	day             string
	provider, model string
	source          usageSource
	usageTotals
}

func (h *LLMUsageHandler) rows(ctx context.Context, month time.Time, user *uuid.UUID, source usageSource) ([]usageRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	all, who := true, uuid.Nil
	if user != nil {
		all, who = false, *user
	}
	rs, err := h.ch.Query(ctx, usageRowsQuery, month, month.AddDate(0, 1, 0), all, who.String(), source == usageAll, source == usagePersonal)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	prices := h.prices.load(ctx)
	var out []usageRow
	for rs.Next() {
		var r usageRow
		if err := rs.Scan(&r.user, &r.vm, &r.day, &r.provider, &r.model,
			&r.Requests, &r.Input, &r.Output, &r.CacheRead, &r.CacheWrite); err != nil {
			return nil, err
		}
		r.source = usageSourceForProvider(r.provider)
		r.provider = llmUsageProvider(r.provider)
		if p, ok := lookupLLMPrice(prices, r.provider, r.model); ok {
			r.Cost = float64(r.Input)*p.input + float64(r.Output)*p.output +
				float64(r.CacheRead)*p.cacheRead + float64(r.CacheWrite)*p.cacheWrite
		} else {
			r.Unpriced = r.Input+r.Output+r.CacheRead+r.CacheWrite > 0
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func llmUsageProvider(metered string) string {
	return strings.TrimSuffix(metered, llmPersonalMeterSuffix)
}

func usageSourceForProvider(metered string) usageSource {
	if strings.HasSuffix(metered, llmPersonalMeterSuffix) {
		return usagePersonal
	}
	return usageGlobal
}

func (h *LLMUsageHandler) report(c *echo.Context, user uuid.UUID, source usageSource) error {
	month, err := usageMonth(c.QueryParam("month"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "month must be YYYY-MM")
	}
	rep := usageReport{Month: month.Format("2006-01"), Source: source, ByModel: []usageByModel{}, ByVM: []usageByVM{}, ByDay: []usageByDay{}}
	if h.ch == nil {
		return c.JSON(http.StatusOK, rep)
	}
	rows, err := h.rows(c.Request().Context(), month, &user, source)
	if err != nil {
		log.Printf("could not read llm usage for %s (is 0003_llm_usage applied?): %v", user, err)
		return c.JSON(http.StatusOK, rep)
	}
	rep.Available = true

	models := map[[3]string]*usageByModel{}
	vms := map[uuid.UUID]*usageByVM{}
	days := map[string]*usageByDay{}
	for _, r := range rows {
		rep.Totals.add(r.usageTotals)
		k := [3]string{string(r.source), r.provider, r.model}
		if models[k] == nil {
			models[k] = &usageByModel{Provider: r.provider, Model: r.model, Source: r.source}
		}
		models[k].add(r.usageTotals)
		if vms[r.vm] == nil {
			vms[r.vm] = &usageByVM{VMID: r.vm.String()}
		}
		vms[r.vm].add(r.usageTotals)
		if days[r.day] == nil {
			days[r.day] = &usageByDay{Date: r.day}
		}
		days[r.day].add(r.usageTotals)
	}
	for _, m := range models {
		rep.ByModel = append(rep.ByModel, *m)
	}
	sort.Slice(rep.ByModel, func(i, j int) bool { return rep.ByModel[i].Output > rep.ByModel[j].Output })
	ids := make([]pgtype.UUID, 0, len(vms))
	for id := range vms {
		ids = append(ids, pgtype.UUID{Bytes: id, Valid: true})
	}
	if len(ids) > 0 {
		names, err := h.q.ListVMNamesByIDs(c.Request().Context(), db.ListVMNamesByIDsParams{
			VmIds: ids, OwnerID: pgtype.UUID{Bytes: user, Valid: true},
		})
		if err != nil {
			log.Printf("could not read names for llm usage VMs: %v", err)
		} else {
			for _, n := range names {
				vms[uuid.UUID(n.ID.Bytes)].VMName = n.Name
			}
		}
	}
	for _, vm := range vms {
		rep.ByVM = append(rep.ByVM, *vm)
	}
	sort.Slice(rep.ByVM, func(i, j int) bool {
		if rep.ByVM[i].Cost != rep.ByVM[j].Cost {
			return rep.ByVM[i].Cost > rep.ByVM[j].Cost
		}
		return rep.ByVM[i].VMID < rep.ByVM[j].VMID
	})
	for _, d := range days {
		rep.ByDay = append(rep.ByDay, *d)
	}
	sort.Slice(rep.ByDay, func(i, j int) bool { return rep.ByDay[i].Date < rep.ByDay[j].Date })
	return c.JSON(http.StatusOK, rep)
}

// @Summary     Your usage of global, personal, or all llm keys for a month
// @Tags        llm
// @Produce     json
// @Param       month query string false "YYYY-MM, defaults to the current month (UTC)"
// @Param       source query string false "global (default), personal, or all"
// @Router      /me/llm-usage [get]
func (h *LLMUsageHandler) Mine(c *echo.Context) error {
	uid, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	source, ok := parseUsageSource(c.QueryParam("source"))
	if !ok {
		return echo.NewHTTPError(http.StatusBadRequest, "source must be global, personal, or all")
	}
	return h.report(c, uuid.UUID(uid.Bytes), source)
}

// @Summary     One user's usage of the global llm keys for a month
// @Tags        admin
// @Produce     json
// @Param       month query string false "YYYY-MM, defaults to the current month (UTC)"
// @Router      /admin/llm-usage/users/{id} [get]
func (h *LLMUsageHandler) ForUser(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid user id")
	}
	return h.report(c, id, usageGlobal)
}

// @Summary     Every user's usage of the global llm keys for a month
// @Tags        admin
// @Produce     json
// @Param       month query string false "YYYY-MM, defaults to the current month (UTC)"
// @Router      /admin/llm-usage [get]
func (h *LLMUsageHandler) All(c *echo.Context) error {
	month, err := usageMonth(c.QueryParam("month"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "month must be YYYY-MM")
	}
	out := map[string]any{"month": month.Format("2006-01"), "available": false, "items": []usageByUser{}}
	if h.ch == nil {
		return c.JSON(http.StatusOK, out)
	}
	ctx := c.Request().Context()
	rows, err := h.rows(ctx, month, nil, usageGlobal)
	if err != nil {
		log.Printf("could not read llm usage (is 0003_llm_usage applied?): %v", err)
		return c.JSON(http.StatusOK, out)
	}

	byUser := map[uuid.UUID]*usageByUser{}
	var ids []pgtype.UUID
	for _, r := range rows {
		if byUser[r.user] == nil {
			byUser[r.user] = &usageByUser{UserID: r.user.String()}
			ids = append(ids, pgtype.UUID{Bytes: r.user, Valid: true})
		}
		byUser[r.user].add(r.usageTotals)
	}
	if names, err := h.q.ListUsernamesByIDs(ctx, ids); err == nil {
		for _, n := range names {
			if u := byUser[uuid.UUID(n.ID.Bytes)]; u != nil {
				u.Username = n.Username
			}
		}
	}

	items := make([]usageByUser, 0, len(byUser))
	for _, u := range byUser {
		items = append(items, *u)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Cost > items[j].Cost })
	out["available"], out["items"] = true, items
	return c.JSON(http.StatusOK, out)
}
