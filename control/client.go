package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

// Keepalive and I/O budgets for client sockets.
const (
	pingInterval = 30 * time.Second
	pongTimeout  = 10 * time.Second
	writeTimeout = 10 * time.Second
	helloTimeout = 15 * time.Second
	// touchInterval throttles last_seen_at writes: a chatty client should not turn
	// into a stream of UPDATEs.
	touchInterval = 30 * time.Second
)

// ClientHandler serves the client-facing endpoints under /api/v1/client. These are
// authenticated by enrollment key or client token, never by the user JWT.
type ClientHandler struct {
	q    *db.Queries
	pool *pgxpool.Pool
	hub  *Hub
	// proxy is carried only to be handed to pushProxyConfig: the generated
	// dproxy.yaml names this server and carries the key its hosts verify with.
	proxy proxyAuthConfig
	// blobs is where the domains' certificates are kept, read on connect so the
	// dpipe config can carry one. Nil when object storage is not configured, which
	// simply means no host is served over tls.
	blobs *blobStore
}

// openEnrollment reports whether a machine may join the fleet unauthenticated.
// Anything that reaches /enroll can then become an client, so this belongs
// behind a network the operator controls.
//
// Read per request from the settings table rather than latched at boot: an
// admin turning it off in the UI has to take effect on the next enrollment
// attempt, not on the next restart. Fails closed if the read fails.
func (h *ClientHandler) openEnrollment(ctx context.Context) bool {
	return boolSetting(ctx, h.q, settingClientOpenEnrollment)
}

// --- enrollment ------------------------------------------------------------

// Enroll trades a valid enrollment key for a long-lived per-client token. The
// key is consumed and the client row written in one transaction, so a failed
// insert never burns a use of a use-limited key.
//
// With open enrollment on, a request may carry no key. That path is deliberately
// narrower than the keyed one: it can register a machine_id the server has not
// seen, and nothing else. Re-enrolling an existing machine rewrites its token,
// and without a key to prove authorization that would be a takeover of any client
// whose machine_id a caller could guess.
func (h *ClientHandler) Enroll(c *echo.Context) error {
	var req proto.EnrollRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Key = strings.TrimSpace(req.Key)
	req.MachineID = strings.TrimSpace(req.MachineID)
	if req.MachineID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "machine_id is required")
	}
	if h.pool == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "database unavailable")
	}

	ctx := c.Request().Context()

	// After the pool check: whether a keyless enrollment is allowed is itself a
	// database read now.
	if req.Key == "" && !h.openEnrollment(ctx) {
		return echo.NewHTTPError(http.StatusBadRequest, "key and machine_id are required")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not start transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.q.WithTx(tx)

	// Null for a keyless enrollment: no key was consumed, so there is none to
	// point at, and the column already allows it.
	var enrolledKeyID pgtype.UUID
	if req.Key != "" {
		key, err := qtx.ConsumeEnrollmentKey(ctx, hashRefresh(req.Key))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Revoked, expired, exhausted and unknown are deliberately
				// indistinguishable so a caller cannot probe which keys exist.
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired enrollment key")
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "could not validate enrollment key")
		}
		enrolledKeyID = key.ID
	} else {
		// In the same transaction as the upsert below, so two simultaneous keyless
		// requests for one machine_id cannot both pass this check.
		switch _, err := qtx.GetClientByMachineID(ctx, req.MachineID); {
		case err == nil:
			return echo.NewHTTPError(http.StatusConflict,
				"this machine is already enrolled; keyless enrollment cannot re-issue its token, so use an enrollment key or delete the client first")
		case !errors.Is(err, pgx.ErrNoRows):
			return echo.NewHTTPError(http.StatusInternalServerError, "could not check machine registration")
		}
	}

	secret, err := newRefreshToken()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not generate client token")
	}

	// With exactly one domain configured there is no choice to make, so the client
	// gets it. With none or several, it enrolls without one and an operator
	// decides. pgx.ErrNoRows covers both of those cases -- see GetSoleDomain.
	var domainID pgtype.UUID
	switch domain, err := qtx.GetSoleDomain(ctx); {
	case err == nil:
		domainID = domain.ID
	case !errors.Is(err, pgx.ErrNoRows):
		return echo.NewHTTPError(http.StatusInternalServerError, "could not resolve the client domain")
	}

	client, err := qtx.UpsertClientByMachineID(ctx, db.UpsertClientByMachineIDParams{
		MachineID:     req.MachineID,
		Hostname:      strings.TrimSpace(req.Hostname),
		TokenHash:     hashRefresh(secret),
		OS:            req.OS,
		OSVersion:     req.OSVersion,
		Arch:          req.Arch,
		ClientVersion: req.Version,
		EnrolledKeyID: enrolledKeyID,
		DomainID:      domainID,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not register client")
	}
	if err := tx.Commit(ctx); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not complete enrollment")
	}

	clientID := uuid.UUID(client.ID.Bytes).String()
	how := "key"
	if req.Key == "" {
		how = "keyless"
	}
	log.Printf("client %s enrolled via %s (machine_id=%s hostname=%s)", clientID, how, client.MachineID, client.Hostname)

	// The raw token leaves the server exactly once, here.
	return c.JSON(http.StatusCreated, proto.EnrollResponse{ClientID: clientID, Token: secret})
}

// --- websocket -------------------------------------------------------------

// Connect upgrades an authenticated client to a persistent WebSocket and holds
// it open until either side goes away.
func (h *ClientHandler) Connect(c *echo.Context) error {
	raw := ""
	if hdr := c.Request().Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		raw = strings.TrimPrefix(hdr, "Bearer ")
	}
	if raw == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing client token")
	}

	// Authenticate before upgrading so a rejected client sees a real HTTP status
	// rather than an opaque socket close.
	reqCtx := c.Request().Context()
	client, err := h.q.GetClientByTokenHash(reqCtx, hashRefresh(raw))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid client token")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not authenticate client")
	}
	if client.Revoked {
		return echo.NewHTTPError(http.StatusUnauthorized, "client has been revoked")
	}

	// InsecureSkipVerify disables the *Origin* check only. dclient is not a
	// browser and sends no Origin header; authentication is the bearer token
	// above, which this does not weaken.
	ws, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil // Accept already wrote a response
	}

	clientID := uuid.UUID(client.ID.Bytes).String()
	h.serveClient(client, clientID, c.RealIP(), ws)
	return nil
}

// serveClient owns the connection for its lifetime.
func (h *ClientHandler) serveClient(client db.Client, clientID, remoteIP string, ws *websocket.Conn) {
	// Detached from the request context: that one is cancelled as soon as the
	// handler returns, which for a hijacked connection is immediately.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := newClientConn(clientID, ws)
	h.hub.add(conn)

	if err := h.q.SetClientOnline(ctx, db.SetClientOnlineParams{ID: client.ID, LastIP: remoteIP}); err != nil {
		log.Printf("client %s: could not mark online: %v", clientID, err)
	}

	defer func() {
		h.hub.remove(conn)
		conn.close(websocket.StatusNormalClosure, "closing")
		// A fresh context: ctx is already cancelled by the time this runs.
		octx, ocancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ocancel()
		if err := h.q.SetClientOffline(octx, client.ID); err != nil {
			log.Printf("client %s: could not mark offline: %v", clientID, err)
		}
		// Anything still pending was waiting on a result frame from the socket that
		// just died. It is never going to arrive, so the row must not keep claiming
		// the create is in progress.
		if err := h.q.FailPendingVMsForClient(octx, db.FailPendingVMsForClientParams{
			ClientID:  client.ID,
			LastError: "the client disconnected before reporting the result",
		}); err != nil {
			log.Printf("client %s: could not fail pending vms: %v", clientID, err)
		}
		log.Printf("client %s disconnected", clientID)
	}()

	go conn.writePump(ctx)

	// Close the socket as soon as the writer gives up (failed ping or write),
	// so the blocking Read below returns instead of hanging.
	go func() {
		<-conn.closed
		cancel()
	}()

	hello, err := h.handshake(ctx, conn, client, clientID)
	if err != nil {
		log.Printf("client %s: handshake failed: %v", clientID, err)
		conn.close(websocket.StatusPolicyViolation, "handshake failed")
		return
	}
	log.Printf("client %s connected (hostname=%s ip=%s)", clientID, client.Hostname, remoteIP)

	// The host may have been offline while destinations were added or removed, so
	// its local.rules is only trustworthy once this server has written it. Sent on
	// every connect rather than only when something changed: dclient's copy is not
	// knowable from here, and rewriting an identical file costs a reload.
	pushSuricataRules(ctx, h.q, h.hub, client.ID)
	// The Corefile is compiled from the same rows and has to move with them, so it
	// goes wherever the ruleset goes. A host holding one of the two at a newer
	// version than the other is a resolver and a ruleset that disagree about what a
	// guest may reach.
	pushCoreDNSConfig(ctx, h.q, h.hub, client.ID)
	// Same reasoning for dproxy.yaml: VMs may have come or gone while the host was
	// away. Cheap to send unconditionally -- the client compares the file it is
	// given against the one on disk and only restarts proxy when they differ.
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
	// And vector.yaml: the settings it is built from may have changed while the
	// host was away, and this is also what installs vector on a host that has
	// just enrolled.
	pushVectorConfig(ctx, h.q, h.hub, client.ID)
	// suricata.yaml is sent here and nowhere else: it is not built from anything
	// that changes while a host is connected, so a connect is the only moment it
	// can be out of date. It needs the pool the hello just carried, which is why
	// it is not sent from anywhere that has only an client id.
	pushSuricataConfig(ctx, h.hub, client.ID, hello.Pool)
	// dpipe.yaml was in the same position until it started carrying the domain's
	// certificate, which changes on its own schedule -- so the issuance and renewal
	// paths send one too. This is still where a host that was offline for a renewal
	// catches up.
	pushDpipeConfig(ctx, h.q, h.blobs, h.hub, client.ID)

	h.readLoop(ctx, conn, client, clientID)
}

// handshake requires `hello` as the very first frame and answers `hello_ack`.
// The facts it carries refresh the client row -- an client that was upgraded or
// renamed since enrollment reports the truth here.
func (h *ClientHandler) handshake(ctx context.Context, conn *clientConn, client db.Client, clientID string) (proto.Hello, error) {
	var hello proto.Hello

	hctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()

	env, err := readEnvelope(hctx, conn.ws)
	if err != nil {
		return hello, err
	}
	if env.Type != proto.TypeHello {
		return hello, errors.New("expected hello, got " + string(env.Type))
	}

	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		return hello, err
	}

	if err := h.q.UpdateClientFacts(hctx, db.UpdateClientFactsParams{
		ID:            client.ID,
		Hostname:      hello.Hostname,
		OS:            hello.OS,
		OSVersion:     hello.OSVersion,
		Arch:          hello.Arch,
		ClientVersion: hello.Version,
	}); err != nil {
		log.Printf("client %s: could not update facts: %v", clientID, err)
	}

	ack, err := proto.NewEnvelope(proto.TypeHelloAck, env.ID, proto.HelloAck{
		ClientID:   clientID,
		ServerTime: time.Now().UTC(),
	})
	if err != nil {
		return hello, err
	}
	// Straight to this connection, not via the hub: if a replacement socket has
	// already displaced us, the ack must not go to it.
	return hello, conn.enqueue(ack)
}

// readLoop drains frames until the connection dies.
func (h *ClientHandler) readLoop(ctx context.Context, conn *clientConn, client db.Client, clientID string) {
	lastTouch := time.Now()

	for {
		env, err := readEnvelope(ctx, conn.ws)
		if err != nil {
			if ctx.Err() == nil && websocket.CloseStatus(err) == -1 {
				log.Printf("client %s: read error: %v", clientID, err)
			}
			conn.close(websocket.StatusNormalClosure, "read loop ended")
			return
		}

		if time.Since(lastTouch) >= touchInterval {
			lastTouch = time.Now()
			if err := h.q.TouchClientLastSeen(ctx, client.ID); err != nil {
				log.Printf("client %s: could not touch last_seen: %v", clientID, err)
			}
		}

		switch env.Type {
		case proto.TypeResult:
			h.handleResult(ctx, client, clientID, env)
		case proto.TypeMetrics:
			h.handleMetrics(ctx, client, clientID, env)
		case proto.TypeInventory:
			h.handleInventory(ctx, client, clientID, env)
		case proto.TypeError:
			var p proto.ErrorPayload
			_ = json.Unmarshal(env.Payload, &p)
			log.Printf("client %s reported an error: %s", clientID, p.Message)
		default:
			log.Printf("client %s: ignoring unexpected frame type %q", clientID, env.Type)
		}
	}
}

// handleResult settles the row the job was created from. The envelope id *is*
// the vms row id, which is what makes the correlation a lookup rather than a
// table of in-flight jobs that a restart would lose.
func (h *ClientHandler) handleResult(ctx context.Context, client db.Client, clientID string, env proto.Envelope) {
	var res proto.JobResult
	if err := json.Unmarshal(env.Payload, &res); err != nil {
		log.Printf("client %s: could not decode the result for job %s: %v", clientID, env.ID, err)
		return
	}
	// A ruleset push settles no row, so it carries no correlation id and must be
	// answered before the id is parsed. There is nothing to record either: the
	// database already holds the policy, and this only says whether the host has
	// caught up with it yet.
	if res.Kind == proto.KindSuricataRules {
		if !res.OK {
			log.Printf("client %s: could not apply the suricata ruleset: %s", clientID, res.Error)
		}
		return
	}
	// A proxy config push settles no row either, for the same reasons.
	if res.Kind == proto.KindProxyConfig {
		if !res.OK {
			log.Printf("client %s: could not apply the proxy config: %s", clientID, res.Error)
		}
		return
	}
	// The whole-file config pushes, same again.
	if res.Kind == proto.KindSuricataConfig || res.Kind == proto.KindDpipeConfig ||
		res.Kind == proto.KindCoreDNSConfig {
		if !res.OK {
			log.Printf("client %s: could not apply the %s config: %s", clientID, res.Kind, res.Error)
		}
		return
	}
	// Same for vector: the settings are already stored, and this only says
	// whether the host has managed to install and start it.
	if res.Kind == proto.KindVectorConfig {
		if !res.OK {
			log.Printf("client %s: could not apply the vector config: %s", clientID, res.Error)
		}
		return
	}

	rowID, err := parseUUID(env.ID)
	if err != nil {
		log.Printf("client %s: result for job %s has an unusable correlation id", clientID, env.ID)
		return
	}

	switch res.Kind {
	case proto.KindVMCreate:
		h.settleCreate(ctx, client, clientID, env.ID, rowID, res)
	case proto.KindVMStop:
		h.settleEnd(ctx, clientID, env.ID, rowID, res, "stopped")
	case proto.KindVMStart:
		h.settleEnd(ctx, clientID, env.ID, rowID, res, "running")
	case proto.KindVMDestroy:
		h.settleDestroy(ctx, clientID, env.ID, rowID, res)
		// The address this VM held goes back to the pool and will be handed to some
		// other guest. Its pass rules have to be gone before that happens, or the
		// new guest inherits an allowlist it was never granted.
		// The proxy entry has to go for the same reason and with the same urgency:
		// until it does, the destroyed VM's owner still has a route pointing at an
		// address that is about to belong to somebody else's guest.
		if res.OK {
			pushSuricataRules(ctx, h.q, h.hub, client.ID)
			pushCoreDNSConfig(ctx, h.q, h.hub, client.ID)
			pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
		}
	default:
		log.Printf("client %s: result for job %s of unknown kind %q", clientID, env.ID, res.Kind)
	}
}

// settleDestroy settles a destroy, which ends one of two ways. An operator's
// destroy leaves the row behind as 'gone', because a VM that was destroyed is
// exactly what somebody will look up afterwards. An owner deleting their own VM
// asked for the record to go too, and marked the row for it before the job was
// sent -- so here the row is dropped outright rather than left in their list as a
// VM they already deleted.
//
// A failed destroy takes neither path: settleEnd records the message and leaves
// the status alone, and the mark is spent, so the guest is still there and still
// theirs to delete again.
func (h *ClientHandler) settleDestroy(ctx context.Context, clientID, jobID string, rowID pgtype.UUID, res proto.JobResult) {
	if !h.hub.TakePurge(jobID) || !res.OK {
		h.settleEnd(ctx, clientID, jobID, rowID, res, "gone")
		return
	}
	if err := h.q.DeleteVM(ctx, rowID); err != nil {
		log.Printf("client %s: could not delete the record of vm %s: %v", clientID, jobID, err)
		// The guest really is gone, so saying so beats leaving the row claiming to be
		// running because a delete failed.
		h.settleEnd(ctx, clientID, jobID, rowID, res, "gone")
		return
	}
	cancelTasksForSubject(ctx, h.q, subjectVM, rowID, "the vm was deleted")
	log.Printf("client %s: vm %s destroyed and its record deleted", clientID, jobID)
}

// settleEnd records the outcome of a start, stop or destroy. On success the status is
// moved now rather than waiting for the next inventory tick, which is what makes
// the button feel like it did something. On failure only the message is stored:
// a stop that failed probably leaves the VM running, and overwriting the status
// would replace a true claim with a guess.
func (h *ClientHandler) settleEnd(ctx context.Context, clientID, jobID string, rowID pgtype.UUID, res proto.JobResult, status string) {
	if !res.OK {
		msg := res.Error
		if msg == "" {
			msg = "the client reported a failure with no message"
		}
		if err := h.q.SetVMLastError(ctx, db.SetVMLastErrorParams{ID: rowID, LastError: msg}); err != nil {
			log.Printf("client %s: could not record the failed %s of vm %s: %v", clientID, res.Kind, jobID, err)
		}
		log.Printf("client %s: %s of vm %s failed: %s", clientID, res.Kind, jobID, msg)
		return
	}
	if err := h.q.SetVMStatus(ctx, db.SetVMStatusParams{ID: rowID, Status: status}); err != nil {
		log.Printf("client %s: could not mark vm %s %s: %v", clientID, jobID, status, err)
		return
	}
	log.Printf("client %s: vm %s is now %s", clientID, jobID, status)
}

func (h *ClientHandler) settleCreate(ctx context.Context, client db.Client, clientID, jobID string, rowID pgtype.UUID, res proto.JobResult) {
	if !res.OK || res.VM == nil {
		msg := res.Error
		if msg == "" {
			msg = "the client reported a failure with no message"
		}
		if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: rowID, LastError: msg}); err != nil {
			log.Printf("client %s: could not record the failed vm %s: %v", clientID, jobID, err)
		}
		log.Printf("client %s: vm %s failed: %s", clientID, jobID, msg)
		return
	}

	// An inventory report can beat the result frame here and adopt the very VM
	// this row is waiting for, which would collide on (client_id, vm_id). Dropping
	// the adopted duplicate and claiming the id must happen together, or a failure
	// in between leaves two rows for one VM.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		log.Printf("client %s: could not start a transaction for vm %s: %v", clientID, jobID, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.q.WithTx(tx)

	if err := qtx.DeleteAdoptedVM(ctx, db.DeleteAdoptedVMParams{
		ClientID: client.ID,
		VMID:     res.VM.ID,
		ID:       rowID,
	}); err != nil {
		log.Printf("client %s: could not clear the adopted duplicate of vm %s: %v", clientID, jobID, err)
		return
	}
	// The name the host reports back is deliberately not applied: it was chosen
	// here, it is unique across the fleet, and it is what the VM's http route is
	// keyed on.
	if err := qtx.MarkVMRunning(ctx, db.MarkVMRunningParams{
		ID:        rowID,
		VMID:      res.VM.ID,
		Boot:      res.VM.Boot,
		CPUs:      int32(res.VM.CPUs),
		MemoryMiB: int32(res.VM.MemoryMiB),
		IP:        res.VM.IP,
	}); err != nil {
		log.Printf("client %s: could not record the running vm %s: %v", clientID, jobID, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("client %s: could not commit the running vm %s: %v", clientID, jobID, err)
		return
	}
	log.Printf("client %s: vm %s running (local id %s, ip %s)", clientID, jobID, res.VM.ID, res.VM.IP)

	// Here rather than at the create request: the VM's address is allocated on the
	// host and is not knowable until this frame, and an entry with no target to
	// point at is not an entry. After the commit, so the generator reads the row
	// this result just wrote.
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
	// The ruleset for the same reason, and it is not optional. A VM with no
	// allowances is denied by a rule naming its address, and its address is what
	// this frame just delivered -- so until the ruleset is regenerated the new
	// guest is covered only by the pool-wide floor, which has to let a syn through
	// on the web ports. The Corefile follows it, as everywhere else.
	pushSuricataRules(ctx, h.q, h.hub, client.ID)
	pushCoreDNSConfig(ctx, h.q, h.hub, client.ID)
}

// handleInventory reconciles the client's report against the registry. This is
// what makes a VM created locally with `dclient vm create` appear in the control
// plane at all, and what notices one that has been removed on the host.
//
// The report is authoritative but not destructive: rows are upserted or marked
// 'gone', never deleted, so a VM that disappeared stays visible.
func (h *ClientHandler) handleInventory(ctx context.Context, client db.Client, clientID string, env proto.Envelope) {
	var inv proto.Inventory
	if err := json.Unmarshal(env.Payload, &inv); err != nil {
		log.Printf("client %s: could not decode inventory: %v", clientID, err)
		return
	}

	seen := make([]string, 0, len(inv.VMs))
	for _, v := range inv.VMs {
		if v.ID == "" {
			continue // nothing to key on; the client should never send this
		}
		seen = append(seen, v.ID)

		status := "stopped"
		started := pgtype.Timestamptz{}
		if v.Running {
			status = "running"
			// The host does not report when the boot happened, so its creation time is
			// the closest honest answer -- and only for a row we are learning about now.
			started = pgtype.Timestamptz{Time: v.CreatedAt, Valid: !v.CreatedAt.IsZero()}
		}
		created := pgtype.Timestamptz{Time: v.CreatedAt, Valid: !v.CreatedAt.IsZero()}
		if !created.Valid {
			created = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}

		// The host's name is only a suggestion here: it was chosen on a machine that
		// cannot see the rest of the fleet, so it may be taken or may not be a name
		// at all, and either way a generated one is used instead. The name only
		// applies if this turns out to be an insert -- the upsert leaves an existing
		// row's name alone, so the name a guest is reachable at does not move on
		// every tick.
		preferred, _ := validateVMName(v.Name)
		// A host that picked a reserved name gets a generated one instead: there is
		// nobody to report the refusal to, and a VM that is really running has to
		// appear either way.
		if vmNameAllowed(ctx, h.q, preferred) != nil {
			preferred = ""
		}
		if err := withAdoptedVMName(ctx, preferred, func(ctx context.Context, name string) error {
			return h.q.UpsertVMFromInventory(ctx, db.UpsertVMFromInventoryParams{
				ClientID:  client.ID,
				VMID:      v.ID,
				Name:      name,
				Status:    status,
				Boot:      v.Boot,
				CPUs:      int32(v.CPUs),
				MemoryMiB: int32(v.MemoryMiB),
				IP:        v.IP,
				CreatedAt: created,
				StartedAt: started,
			})
		}); err != nil {
			log.Printf("client %s: could not record vm %s from inventory: %v", clientID, v.ID, err)
		}
	}

	if err := h.q.MarkMissingVMsGone(ctx, db.MarkMissingVMsGoneParams{
		ClientID: client.ID,
		VmIds:    seen,
	}); err != nil {
		log.Printf("client %s: could not reconcile removed vms: %v", clientID, err)
	}

	// An inventory report is the only way this server hears about a VM destroyed
	// on the host directly, and a route left pointing at its address after it is
	// reissued would hand one user's key to another user's guest. Sent on every
	// tick rather than only when the report changed something: the client restarts
	// proxy only when the file it receives differs from the one on disk, so an
	// unchanged fleet costs a frame and a comparison.
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
	// The egress policy for the same reason and on the same terms. An inventory
	// tick is also how a VM created on the host with `dclient vm create` first gets
	// a row here, and that VM has no allowances -- so it needs the deny that names
	// its address, which cannot be written until this report says the address
	// exists. Both appliers ignore a file identical to the one on disk, so an
	// unchanged fleet costs a frame and a comparison here too.
	pushSuricataRules(ctx, h.q, h.hub, client.ID)
	pushCoreDNSConfig(ctx, h.q, h.hub, client.ID)
}

func (h *ClientHandler) handleMetrics(ctx context.Context, client db.Client, clientID string, env proto.Envelope) {
	var m proto.Metrics
	if err := json.Unmarshal(env.Payload, &m); err != nil {
		log.Printf("client %s: could not decode metrics: %v", clientID, err)
		return
	}
	if err := h.q.UpdateClientMetrics(ctx, db.UpdateClientMetricsParams{
		ID:             client.ID,
		CPUCount:       int32(m.CPUCount),
		CPUPercent:     m.CPUPercent,
		Load1:          m.Load1,
		Load5:          m.Load5,
		Load15:         m.Load15,
		MemTotalBytes:  m.MemTotalBytes,
		MemUsedBytes:   m.MemUsedBytes,
		DiskTotalBytes: m.DiskTotalBytes,
		DiskUsedBytes:  m.DiskUsedBytes,
		UptimeSeconds:  m.UptimeSeconds,
	}); err != nil {
		log.Printf("client %s: could not store metrics: %v", clientID, err)
	}
}

// readEnvelope reads one text frame and decodes it.
func readEnvelope(ctx context.Context, ws *websocket.Conn) (proto.Envelope, error) {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return proto.Envelope{}, err
	}
	var env proto.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return proto.Envelope{}, err
	}
	return env, nil
}
