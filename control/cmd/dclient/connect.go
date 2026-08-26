package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/urfave/cli/v3"

	"control/internal/proto"
)

// Reconnect backoff bounds.
const (
	backoffMin = 1 * time.Second
	backoffMax = 60 * time.Second

	// reportInterval is how often a host snapshot and a VM inventory are pushed.
	// It also doubles as application-level evidence of liveness between websocket
	// pings.
	reportInterval = 30 * time.Second
)

// errTerminal marks a failure that retrying cannot fix -- a revoked or unknown
// credential. A revoked client should stop, not hammer the control plane.
var errTerminal = errors.New("terminal error")

func connectCommand() *cli.Command {
	return &cli.Command{
		Name:  "connect",
		Usage: "enroll if needed, then hold a persistent connection to the control server",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "control-url",
				Usage:    "base URL of the control server, e.g. https://control.example.com",
				Sources:  cli.EnvVars("DCLIENT_CONTROL_URL"),
				Required: true,
			},
			&cli.StringFlag{
				Name:    "key",
				Usage:   "enrollment key; only needed the first time on a machine",
				Sources: cli.EnvVars("DCLIENT_KEY"),
			},
			&cli.BoolFlag{
				Name:    "insecure",
				Usage:   "allow a plain-http control URL and skip TLS certificate verification",
				Sources: cli.EnvVars("DCLIENT_INSECURE"),
			},
			&cli.StringFlag{
				Name:    "state-dir",
				Usage:   "directory holding client.json (default /etc/dclient, or the user config dir)",
				Sources: cli.EnvVars("DCLIENT_STATE_DIR"),
			},
			&cli.StringFlag{
				Name:    "data-dir",
				Usage:   "directory holding images and vms; where pushed vm.create jobs build",
				Sources: cli.EnvVars("DCLIENT_DATA_DIR"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runConnect(cmd.String("control-url"), cmd.String("key"),
				cmd.String("state-dir"), cmd.String("data-dir"), cmd.Bool("insecure"))
		},
	}
}

func runConnect(controlURL, key, stateDir, dataDir string, insecure bool) error {
	base, err := normalizeControlURL(controlURL, insecure)
	if err != nil {
		return err
	}
	if insecure {
		log.Print("WARNING: --insecure is set; plain http is allowed and TLS certificates are NOT verified")
	}

	if stateDir == "" {
		stateDir = defaultStateDir()
	}
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	st, err := loadState(stateDir)
	if err != nil {
		return fmt.Errorf("could not read client state in %s: %w", stateDir, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := httpClient(insecure)

	// Enrolling on every start would burn a use of a use-limited key each time
	// the client restarts, so saved credentials always win over --key.
	if st.Token == "" {
		// A missing key is no longer refused here: whether a keyless enrollment is
		// allowed is the server's decision, not this host's, and the server says so
		// with a 400 that names the missing key.
		if key == "" {
			log.Print("no enrollment key configured; attempting keyless enrollment")
		}
		st, err = enroll(ctx, client, base, key)
		if err != nil {
			return err
		}
		if err := saveState(stateDir, st); err != nil {
			return fmt.Errorf("enrolled but could not save state to %s: %w", stateDir, err)
		}
		log.Printf("enrolled as client %s", st.ClientID)
	} else if key != "" {
		log.Print("already enrolled; ignoring --key")
	}

	return connectLoop(ctx, client, base, st, dataDir)
}

// normalizeControlURL validates the scheme and strips any trailing path so the
// endpoint constants in proto are the single source of truth for paths.
func normalizeControlURL(raw string, insecure bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid --control-url: %w", err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !insecure {
			return nil, errors.New("--control-url uses plain http; pass --insecure to allow it")
		}
	default:
		return nil, fmt.Errorf("--control-url must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("--control-url is missing a host")
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return u, nil
}

func httpClient(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via --insecure
	}
	return &http.Client{Transport: tr}
}

// --- enrollment -------------------------------------------------------------

func enroll(ctx context.Context, client *http.Client, base *url.URL, key string) (state, error) {
	osName, osVersion := osFacts()
	body, err := json.Marshal(proto.EnrollRequest{
		Key:       key,
		MachineID: machineID(),
		Hostname:  hostname(),
		OS:        osName,
		OSVersion: osVersion,
		Arch:      runtime.GOARCH,
		Version:   version,
	})
	if err != nil {
		return state{}, err
	}

	endpoint := base.JoinPath(proto.EnrollPath).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return state{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		return state{}, fmt.Errorf("could not reach the control server: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusUnauthorized {
		return state{}, fmt.Errorf("%w: enrollment key rejected (invalid, expired, revoked or exhausted)", errTerminal)
	}
	// Sent with no key to a server that wants one, or for a machine_id it has
	// already registered. Retrying changes neither, so stop rather than loop.
	if res.StatusCode == http.StatusBadRequest && key == "" {
		return state{}, fmt.Errorf("%w: this server does not allow keyless enrollment; set enrollment_key", errTerminal)
	}
	if res.StatusCode == http.StatusConflict {
		return state{}, fmt.Errorf("%w: this machine is already enrolled on the server; enrol with a key, or delete the client there first", errTerminal)
	}
	if res.StatusCode != http.StatusCreated {
		return state{}, fmt.Errorf("enrollment failed: %s", res.Status)
	}

	var out proto.EnrollResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return state{}, fmt.Errorf("could not decode enrollment response: %w", err)
	}
	if out.Token == "" {
		return state{}, errors.New("enrollment response contained no token")
	}
	return state{ClientID: out.ClientID, Token: out.Token, ControlURL: base.String()}, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// --- connection loop --------------------------------------------------------

func connectLoop(ctx context.Context, client *http.Client, base *url.URL, st state, dataDir string) error {
	backoff := backoffMin
	// Held across reconnects: cpu utilisation is a delta, and throwing the
	// previous sample away on every blip would mean never reporting a rate.
	var cpu cpuSampler

	for {
		start := time.Now()
		err := connectOnce(ctx, client, base, st, dataDir, &cpu)

		switch {
		case ctx.Err() != nil:
			log.Print("shutting down")
			return nil
		case errors.Is(err, errTerminal):
			return err
		case err != nil:
			log.Printf("connection ended: %v", err)
		default:
			log.Print("connection closed by the server")
		}

		// A connection that stayed up is evidence the server is healthy, so the
		// next outage starts backing off from scratch.
		if time.Since(start) > backoffMax {
			backoff = backoffMin
		}

		wait := jitter(backoff)
		log.Printf("reconnecting in %s", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}

		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// jitter spreads reconnects by +/-20% so a fleet that lost the server together
// does not come back in lockstep.
func jitter(d time.Duration) time.Duration {
	delta := float64(d) * 0.2
	return time.Duration(float64(d) - delta + rand.Float64()*2*delta)
}

func connectOnce(ctx context.Context, client *http.Client, base *url.URL, st state, dataDir string, cpu *cpuSampler) error {
	wsURL := *base
	switch wsURL.Scheme {
	case "https":
		wsURL.Scheme = "wss"
	default:
		wsURL.Scheme = "ws"
	}
	endpoint := wsURL.JoinPath(proto.ConnectPath).String()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+st.Token)

	ws, res, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: header,
	})
	if err != nil {
		if res != nil && res.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("%w: client token rejected; this client has been revoked or deleted", errTerminal)
		}
		return err
	}
	defer ws.CloseNow()

	// Everything past the handshake may write concurrently -- a job finishing, a
	// metrics tick -- so from here on the socket is only touched through link.
	l := &link{ws: ws, data: dataDir}

	if err := sendHello(ctx, ws); err != nil {
		return err
	}

	// Bounded: a server that accepts the socket but never acks must not leave us
	// parked here forever.
	hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
	ack, err := readEnvelope(hctx, ws)
	hcancel()
	if err != nil {
		return fmt.Errorf("no hello_ack: %w", err)
	}
	if ack.Type != proto.TypeHelloAck {
		return fmt.Errorf("expected hello_ack, got %q", ack.Type)
	}
	log.Printf("connected to %s as client %s", base.Host, st.ClientID)

	// Jobs outlive the read loop iteration that started them, so cancelling here
	// is what stops an in-flight create from writing to a dead socket.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go l.pushReports(ctx, cpu)

	return l.readLoop(ctx)
}

func sendHello(ctx context.Context, ws *websocket.Conn) error {
	osName, osVersion := osFacts()
	env, err := proto.NewEnvelope(proto.TypeHello, "", proto.Hello{
		Version:   version,
		MachineID: machineID(),
		Hostname:  hostname(),
		OS:        osName,
		OSVersion: osVersion,
		Arch:      runtime.GOARCH,
		// The pool is what the server's suricata.yaml needs for HOME_NET. Read
		// fresh on every connect rather than stored server-side: an operator who
		// renumbers a host and restarts dclient should not have to tell the control
		// plane separately.
		Pool: localPool(),
	})
	if err != nil {
		return err
	}
	return writeEnvelope(ctx, ws, env)
}

// link is the live control connection. The mutex exists because a websocket
// permits exactly one writer at a time and there are now three of them: the
// read loop, the metrics ticker, and every job goroutine reporting its result.
type link struct {
	ws   *websocket.Conn
	data string

	mu sync.Mutex
}

func (l *link) write(ctx context.Context, env proto.Envelope) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return writeEnvelope(ctx, l.ws, env)
}

// readLoop blocks until the socket dies.
func (l *link) readLoop(ctx context.Context) error {
	for {
		env, err := readEnvelope(ctx, l.ws)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}
			return err
		}

		switch env.Type {
		case proto.TypeJob:
			l.handleJob(ctx, env)
		case proto.TypeError:
			var p proto.ErrorPayload
			_ = json.Unmarshal(env.Payload, &p)
			log.Printf("server reported an error: %s", p.Message)
		default:
			log.Printf("ignoring unexpected frame type %q", env.Type)
		}
	}
}

// handleJob dispatches on kind and never blocks the read loop: creating a VM
// downloads images and builds a filesystem, which takes minutes, and the socket
// still has to answer pings and accept further work while that happens.
func (l *link) handleJob(ctx context.Context, env proto.Envelope) {
	var job proto.Job
	if err := json.Unmarshal(env.Payload, &job); err != nil {
		l.reply(ctx, env.ID, proto.JobResult{Error: "could not decode the job: " + err.Error()})
		return
	}

	switch job.Kind {
	case proto.KindVMCreate:
		if job.VM == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no vm spec"})
			return
		}
		go l.createVM(ctx, env.ID, *job.VM)
	case proto.KindVMStop, proto.KindVMStart, proto.KindVMDestroy:
		if job.VMID == "" {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job named no vm"})
			return
		}
		// Also off the read loop: a guest is given time to shut down cleanly, a
		// destroy stops it first, and a boot waits out the startup grace period.
		go l.runVMAction(ctx, env.ID, job.Kind, job.VMID)
	case proto.KindSuricataRules:
		if job.Suricata == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no ruleset"})
			return
		}
		// Off the read loop like the rest: the reload shells into a container, and
		// a slow docker must not stop the socket answering pings.
		go l.applyRules(ctx, env.ID, job.Suricata.Rules)
	case proto.KindProxyConfig:
		if job.Proxy == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no proxy config"})
			return
		}
		// Off the read loop like the rest: this restarts a unit, and a systemctl
		// that blocks must not stop the socket answering pings.
		go l.applyProxy(ctx, env.ID, *job.Proxy)
	case proto.KindVectorConfig:
		if job.Vector == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no vector config"})
			return
		}
		// Off the read loop for a stronger reason than the rest: this one may
		// download a release tarball, which takes as long as the link is slow.
		go l.applyVector(ctx, env.ID, *job.Vector)
	case proto.KindSuricataConfig, proto.KindDpipeConfig, proto.KindCoreDNSConfig:
		if job.File == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no config"})
			return
		}
		// Off the read loop like the rest: each restarts or signals something, and a
		// slow docker or systemctl must not stop the socket answering pings.
		go l.applyHostConfig(ctx, env.ID, job.Kind, job.File.Config, job.DpipeCerts)
	default:
		l.reply(ctx, env.ID, proto.JobResult{
			Kind:  job.Kind,
			Error: fmt.Sprintf("this client does not know how to run a %q job", job.Kind),
		})
	}
}

func (l *link) createVM(ctx context.Context, jobID string, spec proto.VMSpec) {
	log.Printf("job %s: creating a vm", jobID)

	// Progress goes to the local log rather than back up the socket: the control
	// server records outcomes, and streaming a multi-minute build to it would be
	// a second protocol for no one's benefit.
	logf := func(format string, args ...any) {
		log.Printf("job %s: "+format, append([]any{jobID}, args...)...)
	}

	v, err := createVM(ctx, l.data, spec, logf)
	if err != nil {
		log.Printf("job %s: vm create failed: %v", jobID, err)
		l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindVMCreate, Error: err.Error()})
		return
	}

	info := proto.VMInfo{
		ID:        v.ID,
		Name:      v.Name,
		Boot:      string(v.Boot),
		CPUs:      v.CPUs,
		MemoryMiB: v.MemoryMiB,
	}
	if v.Net != nil {
		info.IP = v.Net.IP
	}
	l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindVMCreate, OK: true, VM: &info})
}

// applyRules installs a pushed ruleset. The context is detached for the same
// reason a stop's is: a write that has begun should finish, since abandoning it
// leaves the host enforcing a policy the control plane believes it replaced.
func (l *link) applyRules(ctx context.Context, jobID, rules string) {
	ctx = context.WithoutCancel(ctx)
	if err := applySuricataRules(rules); err != nil {
		log.Printf("job %s: could not apply the suricata ruleset: %v", jobID, err)
		l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindSuricataRules, Error: err.Error()})
		return
	}
	log.Printf("job %s: applied a new suricata ruleset and reloaded it", jobID)
	l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindSuricataRules, OK: true})
}

// applyProxy installs a pushed dproxy.yaml. Detached for the same reason as the
// ruleset: a write that has begun should finish, since abandoning it leaves the
// host routing by a table the control plane believes it replaced.
func (l *link) applyProxy(ctx context.Context, jobID string, cfg proto.ProxyConfig) {
	ctx = context.WithoutCancel(ctx)
	changed, err := applyProxyConfig(ctx, cfg)
	if err != nil {
		log.Printf("job %s: could not apply the proxy config: %v", jobID, err)
		l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindProxyConfig, Error: err.Error()})
		return
	}
	if changed {
		log.Printf("job %s: installed a new proxy config or key and restarted %s", jobID, proxyService)
	}
	l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindProxyConfig, OK: true})
}

// applyHostConfig installs one of the whole-file configs the control server
// owns. Detached like the rest: a write that has begun should finish, since
// abandoning it leaves the host running a config the control plane believes it
// replaced.
func (l *link) applyHostConfig(ctx context.Context, jobID string, kind proto.JobKind, config string, certs *proto.DpipeCerts) {
	ctx = context.WithoutCancel(ctx)

	// dpipe's takes a second argument -- the certificate the config may name,
	// which has to be written before it -- so it is wrapped rather than assigned.
	apply := applySuricataConfig
	name := "suricata"
	switch kind {
	case proto.KindDpipeConfig:
		name = dpipeService
		apply = func(ctx context.Context, config string) (bool, error) {
			return applyDpipeConfig(ctx, config, certs)
		}
	case proto.KindCoreDNSConfig:
		apply, name = applyCoreDNSConfig, corednsContainer
	}

	changed, err := apply(ctx, config)
	if err != nil {
		log.Printf("job %s: could not apply the %s config: %v", jobID, name, err)
		l.reply(ctx, jobID, proto.JobResult{Kind: kind, Error: err.Error()})
		return
	}
	if changed {
		// Deliberately vague about how it took effect: suricata and dpipe are
		// restarted, coredns is signalled to re-read its file in place.
		log.Printf("job %s: installed a new %s config and put it into effect", jobID, name)
	}
	l.reply(ctx, jobID, proto.JobResult{Kind: kind, OK: true})
}

// applyVector installs the vector release the control server named and the
// config built from its settings. Detached like the rest: an install that has
// begun should finish, since abandoning it half way leaves a binary on disk
// that no version marker claims.
func (l *link) applyVector(ctx context.Context, jobID string, cfg proto.VectorConfig) {
	ctx = context.WithoutCancel(ctx)
	changed, err := applyVectorConfig(ctx, l.data, cfg)
	if err != nil {
		log.Printf("job %s: could not apply the vector config: %v", jobID, err)
		l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindVectorConfig, Error: err.Error()})
		return
	}
	if changed {
		log.Printf("job %s: installed vector %s and restarted it", jobID, cfg.Version)
	}
	l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindVectorConfig, OK: true})
}

// runVMAction handles the three jobs that act on a VM which already exists. All
// are idempotent -- stopping a stopped VM, starting a running one -- so a
// retried job is not an error.
//
// The context is deliberately detached: a stop that is still waiting on a guest
// when the control link drops should finish the shutdown rather than abandon a
// half-stopped VM, a destroy that stopped a guest but did not get to its files
// would leave the host holding disks nobody will reclaim, and a boot that is
// past the point of creating a tap should not be abandoned either.
func (l *link) runVMAction(ctx context.Context, jobID string, kind proto.JobKind, vmID string) {
	ctx = context.WithoutCancel(ctx)

	v, err := resolveVM(l.data, vmID)
	if err != nil {
		l.reply(ctx, jobID, proto.JobResult{Kind: kind, Error: err.Error()})
		return
	}
	logf := func(format string, args ...any) {
		log.Printf("job %s: "+format, append([]any{jobID}, args...)...)
	}

	switch kind {
	case proto.KindVMDestroy:
		logf("destroying vm %s (%s)", v.ID, v.Name)
		if err := removeVM(ctx, l.data, v); err != nil {
			logf("destroy failed: %v", err)
			l.reply(ctx, jobID, proto.JobResult{Kind: kind, Error: err.Error()})
			return
		}

	case proto.KindVMStart:
		logf("starting vm %s (%s)", v.ID, v.Name)
		if _, err := startVM(ctx, l.data, v, logf); err != nil {
			logf("start failed: %v", err)
			l.reply(ctx, jobID, proto.JobResult{Kind: kind, Error: err.Error()})
			return
		}

	case proto.KindVMStop:
		logf("stopping vm %s (%s)", v.ID, v.Name)
		if err := stopVM(ctx, l.data, v.ID, defaultStopWait); err != nil {
			logf("stop failed: %v", err)
			l.reply(ctx, jobID, proto.JobResult{Kind: kind, Error: err.Error()})
			return
		}
		// Same order the socket API uses: the guest is down, so the tap, policy and
		// cgroup it held go back. The address stays reserved in vm.json, which is
		// what lets a start put the VM back at the same place. Failing to release
		// does not un-stop the VM, so it is a warning rather than a failed stop.
		if err := teardownVMNetwork(v); err != nil {
			logf("could not fully tear down the network for %s: %v", v.ID, err)
		}
		if err := removeCgroup(v.Cgroup); err != nil {
			logf("could not remove cgroup %s: %v", v.Cgroup, err)
		}
	}

	l.reply(ctx, jobID, proto.JobResult{Kind: kind, OK: true})
}

// reply correlates by the job's envelope id. A failure is reported as a result
// with OK false rather than as an error frame, because an error frame carries
// no correlation and would leave the server's row pending forever.
func (l *link) reply(ctx context.Context, jobID string, res proto.JobResult) {
	env, err := proto.NewEnvelope(proto.TypeResult, jobID, res)
	if err != nil {
		log.Printf("job %s: could not build the result frame: %v", jobID, err)
		return
	}
	// Detached from ctx: a job that failed because the socket died still has
	// nowhere to send this, but one that finished as the daemon shuts down should
	// get its last word out.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := l.write(wctx, env); err != nil {
		log.Printf("job %s: could not report the result: %v", jobID, err)
	}
}

// pushReports sends a host snapshot and a VM inventory on a timer until the
// connection ends. The first pair goes out immediately so a freshly connected
// client is not blank in the fleet view for half a minute -- and so a VM that was
// created locally shows up as soon as the host is reachable.
func (l *link) pushReports(ctx context.Context, cpu *cpuSampler) {
	ticker := time.NewTicker(reportInterval)
	defer ticker.Stop()

	for {
		metrics, err := proto.NewEnvelope(proto.TypeMetrics, "", cpu.collect(l.data))
		if err != nil {
			log.Printf("could not build the metrics frame: %v", err)
			return
		}
		if err := l.write(ctx, metrics); err != nil {
			// The read loop owns the connection's lifetime and will see the same
			// failure; there is nothing useful to do here but stop.
			return
		}

		// A listing failure is worth reporting nothing rather than reporting an
		// empty inventory: the server reads a missing VM as removed, and an
		// unreadable data directory would look like the whole fleet vanished.
		if inv, err := l.inventory(); err != nil {
			log.Printf("could not read the vm inventory: %v", err)
		} else {
			env, err := proto.NewEnvelope(proto.TypeInventory, "", inv)
			if err != nil {
				log.Printf("could not build the inventory frame: %v", err)
				return
			}
			if err := l.write(ctx, env); err != nil {
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// inventory is everything under vms/, with liveness resolved per VM. Reported in
// full every tick: the directory is the truth about what exists, so a snapshot
// is both simpler than a diff and self-correcting when a frame is lost.
func (l *link) inventory() (proto.Inventory, error) {
	vms, err := listVMs(l.data)
	if err != nil {
		return proto.Inventory{}, err
	}
	inv := proto.Inventory{VMs: make([]proto.VMState, 0, len(vms))}
	for _, v := range vms {
		state := proto.VMState{
			VMInfo: proto.VMInfo{
				ID:        v.ID,
				Name:      v.Name,
				Boot:      string(v.Boot),
				CPUs:      v.CPUs,
				MemoryMiB: v.MemoryMiB,
			},
			Running:   vmPID(l.data, v.ID) != 0,
			CreatedAt: v.CreatedAt,
		}
		if v.Net != nil {
			state.IP = v.Net.IP
		}
		inv.VMs = append(inv.VMs, state)
	}
	return inv, nil
}

func writeEnvelope(ctx context.Context, ws *websocket.Conn, env proto.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, b)
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
