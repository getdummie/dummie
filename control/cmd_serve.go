package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/urfave/cli/v3"

	"control/internal/db"
)

// nuxtTarget is the address the Nuxt dev server binds to.
const nuxtTarget = "http://localhost:3000"

func serveCommand() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "start the API server (and, in a dev build, the Nuxt dev server)",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "api-port",
				Value:   1323,
				Usage:   "port for the Echo API server",
				Sources: cli.EnvVars("API_PORT"),
			},
			&cli.StringFlag{
				Name:    "api-host",
				Value:   "0.0.0.0",
				Usage:   "host for the Echo API server",
				Sources: cli.EnvVars("API_HOST"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runServe(cmd.String("api-host"), int(cmd.Int("api-port")))
		},
	}
}

// runServe runs the Echo server, and in a dev build also starts `bun run dev`
// (in ./web-client) as a child process to serve the UI. air sits above this
// process: on a Go rebuild it sends an interrupt, our handler kills the whole
// Nuxt process group, and we exit so the port is free before air re-runs the
// rebuilt binary. A release build has the SPA embedded and starts nothing, but
// does apply its migrations first when APP_ENV=prod.
func runServe(host string, port int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := loadAuthConfig()

	// Before anything opens a connection, and only in prod. See migrateAllUp.
	if cfg.prod {
		if err := migrateAllUp(); err != nil {
			return err
		}
	}

	// Build a lazy pgx pool from DATABASE_URL. pgxpool.New does not dial, so a
	// missing/unreachable DB never blocks startup; the healthcheck pings on demand.
	// A nil pool (no DATABASE_URL) makes the healthcheck report db:false.
	var pool *pgxpool.Pool
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		p, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return fmt.Errorf("failed to build db pool: %w", err)
		}
		pool = p
		defer pool.Close()
	} else {
		log.Print("DATABASE_URL not set; healthcheck will report db:false")
	}

	ch, err := openClickHouse()
	if err != nil {
		return err
	}
	if ch == nil {
		log.Print("CLICKHOUSE_URL not set; healthcheck will report clickhouse:false")
	} else {
		defer ch.Close()
	}

	web, embedded := webRoot()
	if !embedded {
		if err := startNuxtDevServer(ctx); err != nil {
			return err
		}
	}

	return runEchoServer(ctx, host, port, web, pool, ch, cfg, loadProxyAuthConfig(cfg.prod))
}

// startNuxtDevServer launches `bun run dev` and, on interrupt/SIGTERM (Ctrl-C or
// air's rebuild signal), tears down the whole Nuxt process group before exiting.
// SIGKILL as a backstop if it lingers.
func startNuxtDevServer(ctx context.Context) error {
	nuxt := exec.Command("bun", "run", "dev")
	nuxt.Dir = "web-client"
	nuxt.Stdout, nuxt.Stderr = os.Stdout, os.Stderr
	// Own process group so we can signal Nuxt + its vite/node children together.
	nuxt.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := nuxt.Start(); err != nil {
		return fmt.Errorf("failed to start nuxt dev server (is `bun` on PATH?): %w", err)
	}
	log.Printf("nuxt dev server started (pid %d)", nuxt.Process.Pid)

	go func() {
		<-ctx.Done()
		pgid := nuxt.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _, _ = nuxt.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(1500 * time.Millisecond):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		os.Exit(0)
	}()
	return nil
}

// runEchoServer starts the Echo API server: /api/v1/* is handled here, every
// other path is served from the embedded SPA, or reverse-proxied to the Nuxt dev
// server when web is nil.
func runEchoServer(ctx context.Context, host string, port int, web fs.FS, pool *pgxpool.Pool, ch driver.Conn, cfg authConfig, proxyCfg proxyAuthConfig) error {
	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())
	if web != nil {
		e.Use(serveSPA(web))
	} else {
		e.Use(proxyToNuxt(nuxtTarget))
	}

	api := e.Group("/api/v1")
	api.GET("/health", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// The document /docs renders. Public and outside every auth group: it names
	// routes that each enforce their own auth, and docs you need an account to
	// read are useless at the moment you are trying to get one.
	api.GET("/openapi.json", openAPISpec(loadOpenAPISpec()))

	// Auth: password signup/signin, refresh-token rotation, and session teardown.
	q := db.New(pool)
	ah := &AuthHandler{q: q, cfg: cfg}
	api.GET("/signup_status", ah.SignupStatus)
	api.POST("/signup", ah.Signup)

	// Federated sign-in. Public like the rest of this group: these are the routes
	// someone uses to *get* a session, and the flow authenticates itself with the
	// state cookie and the provider's signature rather than with one.
	oidcH := &OIDCHandler{q: q, auth: ah, controlURL: proxyCfg.controlURL}
	api.GET("/oidc/providers", oidcH.ListPublicProviders)
	api.GET("/oidc/:slug/start", oidcH.Start)
	api.GET("/oidc/:slug/callback", oidcH.Callback)
	api.POST("/signin", ah.Signin)
	api.POST("/token_refresh", ah.TokenRefresh)
	api.POST("/signout", ah.Signout)

	// The proxy's login hand-off. Not under /api/ because a browser is redirected
	// here by another host's proxy, and the path is part of that contract.
	e.GET(proxyLoginPath, (&ProxyLoginHandler{q: q, cfg: cfg, proxy: proxyCfg}).Login)

	// The hub only knows about clients connected to *this* process, so any row
	// left 'online' by a previous run is stale.
	hub := NewHub()
	if pool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := q.SetAllClientsOffline(ctx); err != nil {
			log.Printf("could not reset client statuses at startup: %v", err)
		}
		// Same reasoning for VMs: a pending row was waiting on a result frame that
		// belonged to a socket this process never had.
		if err := q.FailAllPendingVMs(ctx, "the control server restarted before the client reported the result"); err != nil {
			log.Printf("could not fail pending vms at startup: %v", err)
		}
		cancel()
	}

	// Background work the control plane owes the future: TTL expiries, and the
	// housekeeping that prunes their audit trail. In this process rather than a
	// worker of its own because every handler ends in a push down a socket the hub
	// holds, and only this process has those.
	//
	// Nil pool means no database, which is the one case there is nothing to poll;
	// starting the runner then would be a log line every second saying so.
	//
	// Both are built before the runner starts: the renewal task calls into the
	// issuer, so a runner polling before it exists would be a nil dereference on
	// whichever tick came first.
	//
	// blobs is hoisted rather than built inline in the admin handler because the
	// client link reads certificates out of it on every connect, and the issuer
	// writes them.
	blobs := loadBlobStore(context.Background())
	certs := newCertIssuer(q, blobs, hub, proxyCfg)

	tasks := newTaskRunner(q, hub, certs)
	if pool != nil {
		runnerCtx, stopRunner := context.WithCancel(context.Background())
		defer stopRunner()
		go tasks.run(runnerCtx)
	} else {
		log.Print("DATABASE_URL not set; scheduled tasks will not run")
	}

	// Admin: user management + refresh-token session management, JWT + admin gated.
	adminH := &AdminHandler{
		q: q, pool: pool, cfg: cfg, hub: hub,
		blobs: blobs, tasks: tasks, certs: certs,
		controlURL: proxyCfg.controlURL,
	}
	admin := api.Group("/admin", adminJWT(cfg))
	admin.GET("/users", adminH.ListUsers)
	admin.POST("/users", adminH.CreateUser)
	admin.GET("/users/:id", adminH.GetUser)
	admin.PUT("/users/:id/quota", adminH.UpdateUserQuota)
	admin.PUT("/users/:id/public_key", adminH.UpdateUserPublicKey)
	admin.DELETE("/users/:id", adminH.DeleteUser)
	admin.GET("/tokens", adminH.ListTokens)
	admin.POST("/tokens/:id/blacklist", adminH.BlacklistToken)
	admin.DELETE("/tokens/:id", adminH.DeleteToken)
	admin.POST("/tokens/cleanup", adminH.CleanupTokens)
	admin.GET("/client-keys", adminH.ListClientKeys)
	admin.POST("/client-keys", adminH.CreateClientKey)
	admin.POST("/client-keys/:id/revoke", adminH.RevokeClientKey)
	admin.DELETE("/client-keys/:id", adminH.DeleteClientKey)
	admin.GET("/clients", adminH.ListClients)
	admin.GET("/clients/:id", adminH.GetClient)
	admin.POST("/clients/:id/revoke", adminH.RevokeClient)
	admin.DELETE("/clients/:id", adminH.DeleteClient)
	admin.PUT("/clients/:id/domain", adminH.SetClientDomain)
	admin.PUT("/clients/:id/services", adminH.UpdateClientServices)
	admin.POST("/clients/:id/services/upgrade", adminH.UpgradeClientServices)
	admin.GET("/clients/:id/vms", adminH.ListClientVMs)
	admin.POST("/clients/:id/vms", adminH.CreateVM)
	admin.GET("/vms", adminH.ListVMs)
	admin.GET("/vms/:id", adminH.GetVM)
	admin.GET("/vms/:id/targets", adminH.ListVMTargets)
	admin.POST("/vms/:id/start", adminH.StartVM)
	admin.POST("/vms/:id/stop", adminH.StopVM)
	admin.POST("/vms/:id/destroy", adminH.DestroyVM)
	admin.DELETE("/vms/:id", adminH.DeleteVM)
	admin.GET("/domains", adminH.ListDomains)
	admin.POST("/domains", adminH.CreateDomain)
	admin.DELETE("/domains/:id", adminH.DeleteDomain)
	admin.GET("/domains/:id/certificate", adminH.GetCertificate)
	admin.PUT("/domains/:id/tls", adminH.UpdateTLS)
	admin.PUT("/domains/:id/certificate", adminH.UploadCertificate)
	admin.POST("/domains/:id/certificate/issue", adminH.IssueCertificate)
	admin.POST("/domains/:id/certificate/continue", adminH.ContinueCertificate)
	admin.POST("/domains/:id/certificate/cancel", adminH.CancelCertificate)
	admin.DELETE("/domains/:id/certificate", adminH.DeleteCertificate)
	admin.GET("/kernels", adminH.ListKernels)
	admin.POST("/kernels", adminH.CreateKernel)
	admin.GET("/kernels/:id", adminH.GetKernel)
	admin.PUT("/kernels/:id/description", adminH.UpdateKernelDescription)
	admin.DELETE("/kernels/:id", adminH.DeleteKernel)
	admin.GET("/osimages", adminH.ListOSImages)
	admin.POST("/osimages", adminH.CreateOSImage)
	admin.GET("/osimages/:id", adminH.GetOSImage)
	admin.PUT("/osimages/:id/description", adminH.UpdateOSImageDescription)
	admin.DELETE("/osimages/:id", adminH.DeleteOSImage)
	admin.GET("/settings", adminH.ListSettings)
	admin.PUT("/settings/:key", adminH.UpdateSetting)
	admin.GET("/oidc/templates", adminH.ListOIDCTemplates)
	admin.GET("/oidc/providers", adminH.ListOIDCProviders)
	admin.POST("/oidc/providers", adminH.CreateOIDCProvider)
	admin.PUT("/oidc/providers/:id", adminH.UpdateOIDCProvider)
	admin.DELETE("/oidc/providers/:id", adminH.DeleteOIDCProvider)
	admin.GET("/tasks", adminH.ListScheduledTasks)
	admin.GET("/tasks/:id", adminH.GetScheduledTask)
	admin.POST("/tasks/:id/cancel", adminH.CancelScheduledTask)
	admin.POST("/tasks/:id/run-now", adminH.RunScheduledTaskNow)

	// Own profile: no id in the path, so the only account either route can reach
	// is the one the JWT names.
	profileH := &ProfileHandler{q: q}
	me := api.Group("/me", userJWT(cfg, q))
	me.GET("", profileH.GetMe)
	me.PUT("", profileH.UpdateMe)

	// Personal access tokens: the credential a script carries to reach the
	// self-service API. Behind denyPAT so a token cannot mint its successor.
	pats := me.Group("/tokens", denyPAT)
	pats.GET("", profileH.ListTokens)
	pats.POST("", profileH.CreateToken)
	pats.POST("/:id/revoke", profileH.RevokeToken)
	pats.DELETE("/:id", profileH.DeleteToken)

	// Self-service VMs: any signed-in account. Every route scopes to the caller.
	userH := &UserHandler{q: q, pool: pool, hub: hub, prod: cfg.prod, proxy: proxyCfg, blobs: adminH.blobs, ch: ch}
	vms := api.Group("/vms", userJWT(cfg, q))
	vms.GET("", userH.ListVMs)
	vms.POST("", userH.CreateVM)
	vms.GET("/quota", userH.GetQuota)
	vms.GET("/hosts", userH.ListHosts)
	vms.GET("/kernels", userH.ListKernels)
	vms.GET("/osimages", userH.ListOSImages)
	vms.GET("/:id", userH.GetVM)
	vms.DELETE("/:id", userH.DeleteVM)
	vms.POST("/:id/start", userH.StartVM)
	vms.POST("/:id/stop", userH.StopVM)
	vms.POST("/:id/destroy", userH.DestroyVM)
	vms.PUT("/:id/ports", userH.UpdatePorts)
	vms.POST("/:id/console-token", userH.ConsoleToken)
	vms.POST("/:id/web-session", userH.WebSession)
	vms.GET("/:id/targets", userH.ListTargets)
	vms.GET("/:id/denied", userH.ListDeniedEgress)
	vms.POST("/:id/targets", userH.CreateTarget)
	vms.POST("/:id/targets/resolve", userH.ResolveTargetHost)
	vms.DELETE("/:id/targets/:target_id", userH.DeleteTarget)

	// Clients: enrollment + the persistent socket the server pushes jobs down.
	// Authenticated by enrollment key / client token, not by the user JWT.
	clientH := &ClientHandler{q: q, pool: pool, hub: hub, proxy: proxyCfg, blobs: blobs}
	ag := api.Group("/client")
	ag.POST("/enroll", clientH.Enroll)
	ag.GET("/connect", clientH.Connect)

	// /ht/ is a deeper healthcheck: it reports API liveness plus DB reachability.
	api.GET("/ht/", func(c *echo.Context) error {
		apiOK := true
		dbOK := false
		chOK := false
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if pool != nil {
			dbOK = pool.Ping(ctx) == nil
		}
		if ch != nil {
			chOK = ch.Ping(ctx) == nil
		}
		// clickhouse is deliberately not in "all": it holds observability data, and
		// a control plane that cannot reach it can still create and run vms.
		body := map[string]any{
			"all":        apiOK && dbOK,
			"db":         dbOK,
			"clickhouse": chOK,
			"api":        apiOK,
		}
		// The task runner is reported but does not count towards "all", for the
		// opposite reason clickhouse does not: a control plane whose poller has
		// stalled serves every request correctly and quietly stops keeping the
		// promise attached to a TTL. That needs to be visible, not to fail a
		// liveness probe that would restart the process into the same state.
		if last := tasks.lastTickAt(); !last.IsZero() {
			body["tasks_last_tick_at"] = last.Format(time.RFC3339)
			body["tasks_ticking"] = time.Since(last) < 30*time.Second
		}
		if pool != nil {
			if overdue, err := tasks.overdue(ctx); err == nil {
				body["tasks_overdue"] = overdue
			}
		}
		return c.JSON(http.StatusOK, body)
	})

	// Not e.Start: that builds a server with ReadTimeout at 30s, which is a
	// deadline on reading the whole request rather than on a stalled one. An OS
	// image is minutes of body, so every upload past the first half-gigabyte died
	// mid-stream with an i/o timeout the handler could only report as a failed
	// upload to object storage.
	//
	// ReadHeaderTimeout keeps what that default was there for -- a client that
	// dribbles headers is still cut off -- and the body is bounded by size instead
	// of by time (see readUploadedBlob). WriteTimeout stays unset: the console
	// streams a VM's serial output over a long-lived response.
	sc := echo.StartConfig{
		Address: host + ":" + strconv.Itoa(port),
		BeforeServeFunc: func(s *http.Server) error {
			s.ReadTimeout = 0
			s.ReadHeaderTimeout = 30 * time.Second
			s.IdleTimeout = 120 * time.Second
			return nil
		},
	}
	if err := sc.Start(ctx, e); err != nil {
		e.Logger.Error("failed to start server", "error", err)
		return err
	}
	return nil
}

// proxyToNuxt proxies every request whose path does NOT start with /api/v1 to
// the Nuxt dev server. API requests fall through to the next handler, as does
// /login: the SPA has no page there, and the proxy hand-off has to be answered
// by this server because only it holds the signing key.
//
// The prefix is the API's own base rather than /api/, because Nuxt owns server
// routes under /api/ too -- @nuxt/content serves its client-side database from
// /api/content/*, and those have to reach the dev server.
func proxyToNuxt(target string) echo.MiddlewareFunc {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("invalid nuxt target %q: %v", target, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "nuxt dev server unavailable: "+e.Error(), http.StatusBadGateway)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if path := c.Request().URL.Path; strings.HasPrefix(path, "/api/v1") || path == proxyLoginPath {
				return next(c)
			}
			proxy.ServeHTTP(c.Response(), c.Request())
			return nil
		}
	}
}
