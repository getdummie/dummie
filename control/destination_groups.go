package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const (
	taskVMGroupExpire = "vm_destination_group.expire"
	subjectVMGroup    = "vm_destination_group"
)

const (
	maxGroupEntries     = 256
	maxGroupDescription = 500
	maxImportGroups     = 500
)

var groupNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type groupEntryDTO struct {
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Transport   string `json:"transport"`
	Ports       string `json:"ports"`
	Note        string `json:"note"`
}

type destinationGroupDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Entries     []groupEntryDTO `json:"entries"`
	VMCount     int64           `json:"vm_count,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type destinationGroupReq struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Entries     []groupEntryDTO `json:"entries"`
}

type vmGroupDTO struct {
	ID          string          `json:"id"`
	GroupID     string          `json:"group_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Entries     []groupEntryDTO `json:"entries"`
	CreatedAt   string          `json:"created_at"`
	ExpiresAt   string          `json:"expires_at"`
}

type vmGroupReq struct {
	GroupID    string `json:"group_id"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type vmGroupExpirePayload struct {
	VMID       string `json:"vm_id"`
	VMName     string `json:"vm_name"`
	Group      string `json:"group"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

func toGroupEntryDTO(e db.DestinationGroupEntry) groupEntryDTO {
	return groupEntryDTO{Kind: e.Kind, Destination: e.Destination, Transport: e.Transport, Ports: e.Ports, Note: e.Note}
}

func entriesByGroup(rows []db.DestinationGroupEntry) map[string][]groupEntryDTO {
	out := map[string][]groupEntryDTO{}
	for _, e := range rows {
		id := uuid.UUID(e.GroupID.Bytes).String()
		out[id] = append(out[id], toGroupEntryDTO(e))
	}
	return out
}

func normalizeGroup(req destinationGroupReq) (destinationGroupReq, []db.CreateVMNetworkTargetParams, error) {
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	req.Description = strings.TrimSpace(req.Description)
	if !groupNamePattern.MatchString(req.Name) {
		return req, nil, errors.New("a group name is 1 to 64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if len(req.Description) > maxGroupDescription {
		return req, nil, fmt.Errorf("the description is over %d characters", maxGroupDescription)
	}
	if len(req.Entries) > maxGroupEntries {
		return req, nil, fmt.Errorf("a group can hold at most %d destinations", maxGroupEntries)
	}
	seen := map[string]bool{}
	out := make([]db.CreateVMNetworkTargetParams, 0, len(req.Entries))
	for i, e := range req.Entries {
		nt, err := normalizeTarget(createTargetReq{
			Kind: e.Kind, Destination: e.Destination, Transport: e.Transport, Ports: e.Ports, Note: e.Note,
		})
		if err != nil {
			return req, nil, fmt.Errorf("destination %d (%q): %w", i+1, strings.TrimSpace(e.Destination), err)
		}
		p := nt.params
		if p.Kind == "ip" && p.Destination == targetEverywhere && p.Transport == "any" && p.Ports == "" {
			return req, nil, fmt.Errorf("a group cannot allow everything (%s)", targetEverywhere)
		}
		key := p.Destination + "\x00" + p.Transport + "\x00" + p.Ports
		if seen[key] {
			return req, nil, fmt.Errorf("%s is listed twice", p.Destination)
		}
		seen[key] = true
		out = append(out, p)
	}
	return req, out, nil
}

func writeGroupEntries(ctx context.Context, q *db.Queries, groupID pgtype.UUID, entries []db.CreateVMNetworkTargetParams) error {
	if err := q.DeleteDestinationGroupEntries(ctx, groupID); err != nil {
		return err
	}
	for _, e := range entries {
		if err := q.CreateDestinationGroupEntry(ctx, db.CreateDestinationGroupEntryParams{
			GroupID: groupID, Kind: e.Kind, Destination: e.Destination,
			Transport: e.Transport, Ports: e.Ports, Note: e.Note,
		}); err != nil {
			return err
		}
	}
	return nil
}

func pushGroupPolicy(ctx context.Context, q *db.Queries, hub *Hub, clients []pgtype.UUID) {
	for _, id := range clients {
		pushSuricataRules(ctx, q, hub, id)
		pushCoreDNSConfig(ctx, q, hub, id)
	}
}

func listGroupDTOs(ctx context.Context, q *db.Queries) ([]destinationGroupDTO, error) {
	groups, err := q.ListDestinationGroups(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := q.ListAllDestinationGroupEntries(ctx)
	if err != nil {
		return nil, err
	}
	byGroup := entriesByGroup(entries)
	out := make([]destinationGroupDTO, 0, len(groups))
	for _, g := range groups {
		id := uuid.UUID(g.ID.Bytes).String()
		items := byGroup[id]
		if items == nil {
			items = []groupEntryDTO{}
		}
		out = append(out, destinationGroupDTO{
			ID: id, Name: g.Name, Description: g.Description, Entries: items, VMCount: g.VmCount,
			CreatedAt: g.CreatedAt.Time.Format(time.RFC3339),
			UpdatedAt: g.UpdatedAt.Time.Format(time.RFC3339),
		})
	}
	return out, nil
}

func groupConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (h *AdminHandler) ListDestinationGroups(c *echo.Context) error {
	items, err := listGroupDTOs(c.Request().Context(), h.q)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list destination groups")
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (h *AdminHandler) CreateDestinationGroup(c *echo.Context) error {
	var req destinationGroupReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req, entries, err := normalizeGroup(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ctx := c.Request().Context()
	var g db.DestinationGroup
	err = inTx(ctx, h.pool, h.q, func(q *db.Queries) error {
		var err error
		g, err = q.CreateDestinationGroup(ctx, db.CreateDestinationGroupParams{Name: req.Name, Description: req.Description})
		if err != nil {
			return err
		}
		return writeGroupEntries(ctx, q, g.ID, entries)
	})
	if err != nil {
		if groupConflict(err) {
			return echo.NewHTTPError(http.StatusConflict, "a group with that name already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the group")
	}
	return c.JSON(http.StatusCreated, map[string]string{"id": uuid.UUID(g.ID.Bytes).String()})
}

func (h *AdminHandler) UpdateDestinationGroup(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid group id")
	}
	var req destinationGroupReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req, entries, err := normalizeGroup(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ctx := c.Request().Context()
	err = inTx(ctx, h.pool, h.q, func(q *db.Queries) error {
		if _, err := q.UpdateDestinationGroup(ctx, db.UpdateDestinationGroupParams{
			ID: id, Name: req.Name, Description: req.Description,
		}); err != nil {
			return err
		}
		return writeGroupEntries(ctx, q, id, entries)
	})
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return echo.NewHTTPError(http.StatusNotFound, "no such group")
		case groupConflict(err):
			return echo.NewHTTPError(http.StatusConflict, "a group with that name already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the group")
	}
	clients, err := h.q.ListClientsForDestinationGroup(ctx, id)
	if err != nil {
		log.Printf("could not read the hosts using destination group %s: %v", req.Name, err)
	}
	pushGroupPolicy(ctx, h.q, h.hub, clients)
	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) DeleteDestinationGroup(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid group id")
	}
	ctx := c.Request().Context()
	clients, err := h.q.ListClientsForDestinationGroup(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the hosts using the group")
	}
	n, err := h.q.DeleteDestinationGroup(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the group")
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "no such group")
	}
	pushGroupPolicy(ctx, h.q, h.hub, clients)
	return c.NoContent(http.StatusNoContent)
}

type importGroupsReq struct {
	Groups []destinationGroupReq `json:"groups"`
}

type importedGroup struct {
	Name    string `json:"name"`
	Renamed bool   `json:"renamed"`
}

// freeGroupName suffixes _1, _2, ... until the name is unused, trimming the
// base so the result still fits the name pattern.
func freeGroupName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	for n := 1; ; n++ {
		suffix := "_" + strconv.Itoa(n)
		base := name
		if len(base)+len(suffix) > 64 {
			base = base[:64-len(suffix)]
		}
		if candidate := base + suffix; !taken[candidate] {
			return candidate
		}
	}
}

func (h *AdminHandler) ImportDestinationGroups(c *echo.Context) error {
	var req importGroupsReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid import file")
	}
	if len(req.Groups) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "the file has no groups in it")
	}
	if len(req.Groups) > maxImportGroups {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("at most %d groups can be imported at once", maxImportGroups))
	}
	type parsed struct {
		req     destinationGroupReq
		entries []db.CreateVMNetworkTargetParams
	}
	all := make([]parsed, 0, len(req.Groups))
	for i, g := range req.Groups {
		n, entries, err := normalizeGroup(g)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("group %d (%q): %s", i+1, strings.TrimSpace(g.Name), err.Error()))
		}
		all = append(all, parsed{n, entries})
	}

	ctx := c.Request().Context()
	var out []importedGroup
	err := inTx(ctx, h.pool, h.q, func(q *db.Queries) error {
		names, err := q.ListDestinationGroupNames(ctx)
		if err != nil {
			return err
		}
		taken := make(map[string]bool, len(names))
		for _, n := range names {
			taken[n] = true
		}
		out = make([]importedGroup, 0, len(all))
		for _, p := range all {
			name := freeGroupName(p.req.Name, taken)
			taken[name] = true
			g, err := q.CreateDestinationGroup(ctx, db.CreateDestinationGroupParams{Name: name, Description: p.req.Description})
			if err != nil {
				return err
			}
			if err := writeGroupEntries(ctx, q, g.ID, p.entries); err != nil {
				return err
			}
			out = append(out, importedGroup{Name: name, Renamed: name != p.req.Name})
		}
		return nil
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not import the groups")
	}
	return c.JSON(http.StatusOK, map[string]any{"items": out})
}

// @Summary     List destination groups
// @Description Named sets of destinations an admin maintains. Attach one to a VM with /vms/{id}/groups; edits to a group reach every VM it is attached to.
// @Tags        egress
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string][]destinationGroupDTO
// @Failure     401 {object} apiError
// @Router      /destination-groups [get]
func (h *UserHandler) ListDestinationGroups(c *echo.Context) error {
	items, err := listGroupDTOs(c.Request().Context(), h.q)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list destination groups")
	}
	for i := range items {
		items[i].VMCount = 0
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

func listVMGroups(ctx context.Context, q *db.Queries, vmID pgtype.UUID) ([]vmGroupDTO, error) {
	rows, err := q.ListVMDestinationGroups(ctx, vmID)
	if err != nil {
		return nil, err
	}
	entries, err := q.ListDestinationGroupEntriesForVM(ctx, vmID)
	if err != nil {
		return nil, err
	}
	byGroup := entriesByGroup(entries)
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	deadlines := liveTaskDeadlines(ctx, q, subjectVMGroup, ids)
	out := make([]vmGroupDTO, 0, len(rows))
	for _, r := range rows {
		id := uuid.UUID(r.ID.Bytes).String()
		groupID := uuid.UUID(r.GroupID.Bytes).String()
		items := byGroup[groupID]
		if items == nil {
			items = []groupEntryDTO{}
		}
		d := vmGroupDTO{
			ID: id, GroupID: groupID, Name: r.Name, Description: r.Description, Entries: items,
			CreatedAt: r.CreatedAt.Time.Format(time.RFC3339),
		}
		if at, ok := deadlines[id]; ok {
			d.ExpiresAt = at.Format(time.RFC3339)
		}
		out = append(out, d)
	}
	return out, nil
}

func attachGroup(ctx context.Context, q *db.Queries, vm db.Vm, groupID pgtype.UUID, groupName string, ttlSeconds int64) (db.VmDestinationGroup, error) {
	ttl, err := parseTTLSeconds(ttlSeconds)
	if err != nil {
		return db.VmDestinationGroup{}, err
	}
	a, err := q.AttachDestinationGroupToVM(ctx, db.AttachDestinationGroupToVMParams{VMID: vm.ID, GroupID: groupID})
	if err != nil || ttl == 0 {
		return a, err
	}
	_, err = scheduleGroupExpiry(ctx, q, vm, a.ID, groupName, ttl, ttlSeconds)
	return a, err
}

func scheduleGroupExpiry(ctx context.Context, q *db.Queries, vm db.Vm, attachmentID pgtype.UUID, groupName string, ttl time.Duration, ttlSeconds int64) (db.ScheduledTask, error) {
	return scheduleTask(ctx, q, scheduleTaskParams{
		Kind:        taskVMGroupExpire,
		SubjectKind: subjectVMGroup,
		SubjectID:   attachmentID,
		Payload: vmGroupExpirePayload{
			VMID: uuid.UUID(vm.ID.Bytes).String(), VMName: vm.Name, Group: groupName, TTLSeconds: ttlSeconds,
		},
		Reason:    fmt.Sprintf("temporary access to group %s for %s", groupName, formatTTL(ttl)),
		CreatedBy: vm.CreatedBy,
		After:     ttl,
	})
}

type groupAttachment struct {
	id         pgtype.UUID
	name       string
	ttlSeconds int64
}

// groupsForCreate checks every group a create or recipe asks for before
// anything is written.
func groupsForCreate(ctx context.Context, q *db.Queries, req []vmGroupReq) ([]groupAttachment, error) {
	seen := map[string]bool{}
	var out []groupAttachment
	for _, r := range req {
		id, err := parseUUID(strings.TrimSpace(r.GroupID))
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid destination group id")
		}
		key := uuid.UUID(id.Bytes).String()
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := parseTTLSeconds(r.TTLSeconds); err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		g, err := q.GetDestinationGroup(ctx, id)
		if err != nil {
			return nil, notFoundOr(err, "a destination group in this request no longer exists", "could not read the destination group")
		}
		out = append(out, groupAttachment{id: id, name: g.Name, ttlSeconds: r.TTLSeconds})
	}
	return out, nil
}

// @Summary     Attach a destination group
// @Description Allows every destination in the group, and keeps following the group as an admin edits it. Set ttl_seconds to have the control plane detach it on its own.
// @Tags        egress
// @Accept      json
// @Security    BearerAuth
// @Param       id   path string     true "vm id" format(uuid)
// @Param       body body vmGroupReq true "group_id is required"
// @Success     201 "attached"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "that group is already attached"
// @Router      /vms/{id}/groups [post]
func (h *UserHandler) AttachGroup(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	var req vmGroupReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	groups, err := groupsForCreate(ctx, h.q, []vmGroupReq{req})
	if err != nil {
		return err
	}
	g := groups[0]
	err = inTx(ctx, h.pool, h.q, func(q *db.Queries) error {
		_, err := attachGroup(ctx, q, vm, g.id, g.name, g.ttlSeconds)
		return err
	})
	if err != nil {
		if groupConflict(err) {
			return echo.NewHTTPError(http.StatusConflict, "that group is already attached")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not attach the group")
	}
	pushSuricataRules(ctx, h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(ctx, h.q, h.hub, vm.ClientID)
	return c.NoContent(http.StatusCreated)
}

// @Summary     Change how long a group stays attached
// @Description Cancels any pending expiry and starts a fresh one from ttl_seconds; 0 keeps the group attached until it is removed.
// @Tags        egress
// @Accept      json
// @Security    BearerAuth
// @Param       id       path string     true "vm id" format(uuid)
// @Param       group_id path string     true "attachment id" format(uuid)
// @Param       body     body vmGroupReq true "only ttl_seconds is read"
// @Success     204 "saved"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/groups/{group_id} [put]
func (h *UserHandler) UpdateGroupTTL(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	attachmentID, err := parseUUID(c.Param("group_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid group id")
	}
	var req vmGroupReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ttl, err := parseTTLSeconds(req.TTLSeconds)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ctx := c.Request().Context()
	row, err := h.q.GetVMDestinationGroupForExpiry(ctx, attachmentID)
	if err != nil || row.VMID != vm.ID {
		return echo.NewHTTPError(http.StatusNotFound, "no such group on this vm")
	}
	cancelTasksForSubject(ctx, h.q, subjectVMGroup, attachmentID, "the group's ttl was changed")
	if ttl > 0 {
		if _, err := scheduleGroupExpiry(ctx, h.q, vm, attachmentID, row.GroupName, ttl, req.TTLSeconds); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not schedule the expiry")
		}
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Detach a destination group
// @Description Cancels any pending expiry on it and pushes the new policy to the host.
// @Tags        egress
// @Security    BearerAuth
// @Param       id       path string true "vm id" format(uuid)
// @Param       group_id path string true "attachment id" format(uuid)
// @Success     204 "removed"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/groups/{group_id} [delete]
func (h *UserHandler) DetachGroup(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	attachmentID, err := parseUUID(c.Param("group_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid group id")
	}
	ctx := c.Request().Context()
	n, err := h.q.DeleteVMDestinationGroup(ctx, db.DeleteVMDestinationGroupParams{ID: attachmentID, VMID: vm.ID})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not detach the group")
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "no such group on this vm")
	}
	cancelTasksForSubject(ctx, h.q, subjectVMGroup, attachmentID, "the group was detached before its ttl ran out")
	pushSuricataRules(ctx, h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(ctx, h.q, h.hub, vm.ClientID)
	return c.NoContent(http.StatusNoContent)
}

func handleVMGroupExpire(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	if !t.SubjectID.Valid {
		return taskFailed("this task names no group attachment to expire")
	}
	row, err := r.q.GetVMDestinationGroupForExpiry(ctx, t.SubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskCancelled("the group had already been detached, so there was nothing to expire")
		}
		return taskRetry(taskExpireRetry, "could not read the group attachment: %v", err)
	}
	if _, err := r.q.DeleteVMDestinationGroupByID(ctx, t.SubjectID); err != nil {
		return taskRetry(taskExpireRetry, "could not detach the group: %v", err)
	}
	pushSuricataRules(ctx, r.q, r.hub, row.ClientID)
	pushCoreDNSConfig(ctx, r.q, r.hub, row.ClientID)
	return taskDone("detached group %s from %s", row.GroupName, row.VMName)
}

// groupActivity reports each attached group as one item keyed by its
// attachment id, merging what every entry in it saw.
func groupActivity(groups []vmGroupDTO, seen []egressSeen) []targetActivityDTO {
	out := make([]targetActivityDTO, 0, len(groups))
	for _, g := range groups {
		attachmentID, _ := parseUUID(g.ID)
		created, _ := time.Parse(time.RFC3339, g.CreatedAt)
		synthetic := make([]db.VmNetworkTarget, 0, len(g.Entries))
		for _, e := range g.Entries {
			synthetic = append(synthetic, db.VmNetworkTarget{
				ID: attachmentID, Kind: e.Kind, Destination: e.Destination, Transport: e.Transport, Ports: e.Ports,
				CreatedAt: pgtype.Timestamptz{Time: created, Valid: true},
			})
		}
		merged := targetActivityDTO{TargetID: g.ID, Hosts: []connectedHostDTO{}}
		hosts := map[string]string{}
		for _, a := range targetActivity(synthetic, seen) {
			if a.LastConnected > merged.LastConnected {
				merged.LastConnected = a.LastConnected
			}
			for _, h := range a.Hosts {
				if h.LastSeen > hosts[h.Host] {
					hosts[h.Host] = h.LastSeen
				}
			}
		}
		for host, at := range hosts {
			merged.Hosts = append(merged.Hosts, connectedHostDTO{Host: host, LastSeen: at})
		}
		sortConnectedHosts(merged.Hosts)
		out = append(out, merged)
	}
	return out
}
