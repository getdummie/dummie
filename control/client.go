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

const (
	pingInterval = 30 * time.Second
	pongTimeout  = 10 * time.Second
	writeTimeout = 10 * time.Second
	helloTimeout = 15 * time.Second
	touchInterval = 30 * time.Second
)

type ClientHandler struct {
	q    *db.Queries
	pool *pgxpool.Pool
	hub  *Hub
	proxy proxyAuthConfig
	blobs *blobStore
}

func (h *ClientHandler) openEnrollment(ctx context.Context) bool {
	return boolSetting(ctx, h.q, settingClientOpenEnrollment)
}

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

	if req.Key == "" && !h.openEnrollment(ctx) {
		return echo.NewHTTPError(http.StatusBadRequest, "key and machine_id are required")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not start transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.q.WithTx(tx)

	var enrolledKeyID pgtype.UUID
	if req.Key != "" {
		key, err := qtx.ConsumeEnrollmentKey(ctx, hashRefresh(req.Key))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired enrollment key")
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "could not validate enrollment key")
		}
		enrolledKeyID = key.ID
	} else {
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

	return c.JSON(http.StatusCreated, proto.EnrollResponse{ClientID: clientID, Token: secret})
}

func (h *ClientHandler) Connect(c *echo.Context) error {
	raw := ""
	if hdr := c.Request().Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		raw = strings.TrimPrefix(hdr, "Bearer ")
	}
	if raw == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing client token")
	}

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

	ws, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil
	}

	clientID := uuid.UUID(client.ID.Bytes).String()
	h.serveClient(client, clientID, c.RealIP(), ws)
	return nil
}

func (h *ClientHandler) serveClient(client db.Client, clientID, remoteIP string, ws *websocket.Conn) {
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
		octx, ocancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ocancel()
		if err := h.q.SetClientOffline(octx, client.ID); err != nil {
			log.Printf("client %s: could not mark offline: %v", clientID, err)
		}
		if err := h.q.FailPendingVMsForClient(octx, db.FailPendingVMsForClientParams{
			ClientID:  client.ID,
			LastError: "the client disconnected before reporting the result",
		}); err != nil {
			log.Printf("client %s: could not fail pending vms: %v", clientID, err)
		}
		log.Printf("client %s disconnected", clientID)
	}()

	go conn.writePump(ctx)

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

	pushServicesConfig(ctx, h.q, h.hub, client, false)
	pushSuricataRules(ctx, h.q, h.hub, client.ID)
	pushCoreDNSConfig(ctx, h.q, h.hub, client.ID)
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
	pushVectorConfig(ctx, h.q, h.hub, client.ID)
	pushSuricataConfig(ctx, h.hub, client.ID, hello.Pool)
	pushDpipeConfig(ctx, h.q, h.blobs, h.hub, client.ID)

	h.readLoop(ctx, conn, client, clientID)
}

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
	recordInstalledVersions(hctx, h.q, client.ID, clientID, hello.Services)

	ack, err := proto.NewEnvelope(proto.TypeHelloAck, env.ID, proto.HelloAck{
		ClientID:   clientID,
		ServerTime: time.Now().UTC(),
	})
	if err != nil {
		return hello, err
	}
	return hello, conn.enqueue(ack)
}

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

func (h *ClientHandler) handleResult(ctx context.Context, client db.Client, clientID string, env proto.Envelope) {
	var res proto.JobResult
	if err := json.Unmarshal(env.Payload, &res); err != nil {
		log.Printf("client %s: could not decode the result for job %s: %v", clientID, env.ID, err)
		return
	}
	// A job someone is still holding an http request open for is answered there
	// rather than recorded here.
	if h.hub.Deliver(env.ID, res) {
		return
	}
	if res.Kind == proto.KindSuricataRules {
		if !res.OK {
			log.Printf("client %s: could not apply the suricata ruleset: %s", clientID, res.Error)
		}
		return
	}
	if res.Kind == proto.KindProxyConfig {
		if !res.OK {
			log.Printf("client %s: could not apply the proxy config: %s", clientID, res.Error)
		}
		return
	}
	if res.Kind == proto.KindSuricataConfig || res.Kind == proto.KindDpipeConfig ||
		res.Kind == proto.KindCoreDNSConfig {
		if !res.OK {
			log.Printf("client %s: could not apply the %s config: %s", clientID, res.Kind, res.Error)
		}
		return
	}
	if res.Kind == proto.KindVectorConfig {
		if !res.OK {
			log.Printf("client %s: could not apply the vector config: %s", clientID, res.Error)
		}
		return
	}
	if res.Kind == proto.KindServicesConfig {
		if !res.OK {
			log.Printf("client %s: could not apply the services config: %s", clientID, res.Error)
		}
		recordInstalledVersions(ctx, h.q, client.ID, clientID, res.Services)
		return
	}

	rowID, err := parseUUID(env.ID)
	if err != nil {
		log.Printf("client %s: result for job %s has an unusable correlation id", clientID, env.ID)
		return
	}

	if res.Kind == proto.KindCustomCert {
		h.settleCustomCert(ctx, client, clientID, rowID, res)
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
		if res.OK {
			pushSuricataRules(ctx, h.q, h.hub, client.ID)
			pushCoreDNSConfig(ctx, h.q, h.hub, client.ID)
			pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
		}
	default:
		log.Printf("client %s: result for job %s of unknown kind %q", clientID, env.ID, res.Kind)
	}
}

func (h *ClientHandler) settleCustomCert(ctx context.Context, client db.Client, clientID string, rowID pgtype.UUID, res proto.JobResult) {
	row, err := h.q.GetCustomDomain(ctx, rowID)
	if err != nil {
		log.Printf("client %s: a certificate came back for a custom domain that is gone: %v", clientID, err)
		return
	}

	fail := func(detail string) {
		log.Printf("client %s: could not obtain a certificate for %s: %s", clientID, row.Domain, detail)
		if row.Status == customDomainActive {
			return
		}
		if _, err := h.q.SetCustomDomainStatus(ctx, db.SetCustomDomainStatusParams{
			ID: row.ID, Status: customDomainFailed, LastError: detail,
		}); err != nil {
			log.Printf("could not record the custom domain failure: %v", err)
		}
	}

	if !res.OK {
		fail(res.Error)
		return
	}
	if res.Cert == nil || res.Cert.Cert == "" || res.Cert.Key == "" {
		fail("the host reported success but sent no certificate")
		return
	}
	if err := storeCustomCert(ctx, h.q, h.blobs, h.hub, h.proxy, row, client.ID,
		res.Cert.Cert, res.Cert.Key); err != nil {
		fail(err.Error())
		return
	}
}

func (h *ClientHandler) settleDestroy(ctx context.Context, clientID, jobID string, rowID pgtype.UUID, res proto.JobResult) {
	if !h.hub.TakePurge(jobID) || !res.OK {
		h.settleEnd(ctx, clientID, jobID, rowID, res, "gone")
		return
	}
	if err := h.q.DeleteVM(ctx, rowID); err != nil {
		log.Printf("client %s: could not delete the record of vm %s: %v", clientID, jobID, err)
		h.settleEnd(ctx, clientID, jobID, rowID, res, "gone")
		return
	}
	cancelTasksForSubject(ctx, h.q, subjectVM, rowID, "the vm was deleted")
	log.Printf("client %s: vm %s destroyed and its record deleted", clientID, jobID)
}

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

	pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
	pushSuricataRules(ctx, h.q, h.hub, client.ID)
	pushCoreDNSConfig(ctx, h.q, h.hub, client.ID)
}

func (h *ClientHandler) handleInventory(ctx context.Context, client db.Client, clientID string, env proto.Envelope) {
	var inv proto.Inventory
	if err := json.Unmarshal(env.Payload, &inv); err != nil {
		log.Printf("client %s: could not decode inventory: %v", clientID, err)
		return
	}

	seen := make([]string, 0, len(inv.VMs))
	for _, v := range inv.VMs {
		if v.ID == "" {
			continue
		}
		seen = append(seen, v.ID)

		status := "stopped"
		started := pgtype.Timestamptz{}
		if v.Running {
			status = "running"
			started = pgtype.Timestamptz{Time: v.CreatedAt, Valid: !v.CreatedAt.IsZero()}
		}
		created := pgtype.Timestamptz{Time: v.CreatedAt, Valid: !v.CreatedAt.IsZero()}
		if !created.Valid {
			created = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}

		preferred, _ := validateVMName(v.Name)
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

	pushProxyConfig(ctx, h.q, h.hub, h.proxy, client.ID)
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
