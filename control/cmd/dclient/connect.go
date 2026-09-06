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

const (
	backoffMin = 1 * time.Second
	backoffMax = 60 * time.Second

	reportInterval = 30 * time.Second
)

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

	if st.Token == "" {
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

func connectLoop(ctx context.Context, client *http.Client, base *url.URL, st state, dataDir string) error {
	backoff := backoffMin
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

	l := &link{ws: ws, data: dataDir}

	if err := sendHello(ctx, ws); err != nil {
		return err
	}

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
		Pool: localPool(),
		Services: installedServicesState(ctx),
	})
	if err != nil {
		return err
	}
	return writeEnvelope(ctx, ws, env)
}

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
		go l.runVMAction(ctx, env.ID, job.Kind, job.VMID)
	case proto.KindSuricataRules:
		if job.Suricata == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no ruleset"})
			return
		}
		go l.applyRules(ctx, env.ID, job.Suricata.Rules)
	case proto.KindProxyConfig:
		if job.Proxy == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no proxy config"})
			return
		}
		go l.applyProxy(ctx, env.ID, *job.Proxy)
	case proto.KindVectorConfig:
		if job.Vector == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no vector config"})
			return
		}
		go l.applyVector(ctx, env.ID, *job.Vector)
	case proto.KindServicesConfig:
		if job.Services == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no services config"})
			return
		}
		go l.applyServices(ctx, env.ID, *job.Services)
	case proto.KindCustomCert:
		if job.CustomCert == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no certificate order"})
			return
		}
		go l.obtainCert(ctx, env.ID, *job.CustomCert)
	case proto.KindCacheReport:
		go l.reportImages(ctx, env.ID)
	case proto.KindCachePurge:
		if job.Cache == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job named no cache files"})
			return
		}
		go l.purgeImages(ctx, env.ID, job.Cache.Names)
	case proto.KindSuricataConfig, proto.KindDpipeConfig, proto.KindCoreDNSConfig:
		if job.File == nil {
			l.reply(ctx, env.ID, proto.JobResult{Kind: job.Kind, Error: "job carried no config"})
			return
		}
		go l.applyHostConfig(ctx, env.ID, job.Kind, job.File.Config, job.DpipeCerts, job.DpipeKeys)
	default:
		l.reply(ctx, env.ID, proto.JobResult{
			Kind:  job.Kind,
			Error: fmt.Sprintf("this client does not know how to run a %q job", job.Kind),
		})
	}
}

func (l *link) createVM(ctx context.Context, jobID string, spec proto.VMSpec) {
	log.Printf("job %s: creating a vm", jobID)

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

func (l *link) obtainCert(ctx context.Context, jobID string, order proto.CustomCertOrder) {
	ctx = context.WithoutCancel(ctx)
	log.Printf("job %s: obtaining a certificate for %s", jobID, order.Domain)

	cert, key, err := obtainCustomCert(order)
	if err != nil {
		log.Printf("job %s: could not obtain a certificate for %s: %v", jobID, order.Domain, err)
		l.reply(ctx, jobID, proto.JobResult{
			Kind: proto.KindCustomCert, Domain: order.Domain, Error: err.Error(),
		})
		return
	}
	log.Printf("job %s: obtained a certificate for %s", jobID, order.Domain)
	l.reply(ctx, jobID, proto.JobResult{
		Kind: proto.KindCustomCert, OK: true, Domain: order.Domain,
		Cert: &proto.IssuedCert{Cert: cert, Key: key},
	})
}

func (l *link) reportImages(ctx context.Context, jobID string) {
	ctx = context.WithoutCancel(ctx)
	entries, err := cacheReport(l.data)
	if err != nil {
		log.Printf("job %s: could not read the image cache: %v", jobID, err)
		l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindCacheReport, Error: err.Error()})
		return
	}
	l.reply(ctx, jobID, proto.JobResult{
		Kind: proto.KindCacheReport, OK: true,
		Cache: &proto.CachePurgeResult{Entries: entries},
	})
}

func (l *link) purgeImages(ctx context.Context, jobID string, names []string) {
	ctx = context.WithoutCancel(ctx)
	log.Printf("job %s: purging %d file(s) from the image cache", jobID, len(names))

	res, err := purgeCache(l.data, names)
	if err != nil {
		log.Printf("job %s: could not purge the image cache: %v", jobID, err)
		l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindCachePurge, Error: err.Error(), Cache: &res})
		return
	}
	log.Printf("job %s: removed %d file(s), freeing %d MiB", jobID, len(res.Removed), res.FreedBytes>>20)
	l.reply(ctx, jobID, proto.JobResult{Kind: proto.KindCachePurge, OK: true, Cache: &res})
}

func (l *link) applyHostConfig(ctx context.Context, jobID string, kind proto.JobKind, config string, certs *proto.DpipeCerts, keys *proto.DpipeSSHKeys) {
	ctx = context.WithoutCancel(ctx)

	apply := applySuricataConfig
	name := "suricata"
	switch kind {
	case proto.KindDpipeConfig:
		name = dpipeService
		apply = func(ctx context.Context, config string) (bool, error) {
			return applyDpipeConfig(ctx, config, certs, keys)
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
		log.Printf("job %s: installed a new %s config and put it into effect", jobID, name)
	}
	l.reply(ctx, jobID, proto.JobResult{Kind: kind, OK: true})
}

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

func (l *link) applyServices(ctx context.Context, jobID string, want proto.ServicesConfig) {
	ctx = context.WithoutCancel(ctx)
	upgradeSelf, err := applyServicesConfig(ctx, l.data, want)

	res := proto.JobResult{
		Kind:     proto.KindServicesConfig,
		Services: installedServicesState(ctx),
	}
	if err != nil {
		log.Printf("job %s: could not apply the services config: %v", jobID, err)
		res.Error = err.Error()
	} else {
		res.OK = true
	}
	l.reply(ctx, jobID, res)

	if upgradeSelf {
		restartSelf(ctx)
	}
}

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
		if err := teardownVMNetwork(v); err != nil {
			logf("could not fully tear down the network for %s: %v", v.ID, err)
		}
		if err := removeCgroup(v.Cgroup); err != nil {
			logf("could not remove cgroup %s: %v", v.Cgroup, err)
		}
	}

	l.reply(ctx, jobID, proto.JobResult{Kind: kind, OK: true})
}

func (l *link) reply(ctx context.Context, jobID string, res proto.JobResult) {
	env, err := proto.NewEnvelope(proto.TypeResult, jobID, res)
	if err != nil {
		log.Printf("job %s: could not build the result frame: %v", jobID, err)
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := l.write(wctx, env); err != nil {
		log.Printf("job %s: could not report the result: %v", jobID, err)
	}
}

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
			return
		}

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
