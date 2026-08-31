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

func runServe(host string, port int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := loadAuthConfig()

	if cfg.prod {
		if err := migrateAllUp(); err != nil {
			return err
		}
	}

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

func startNuxtDevServer(ctx context.Context) error {
	nuxt := exec.Command("bun", "run", "dev")
	nuxt.Dir = "web-client"
	nuxt.Stdout, nuxt.Stderr = os.Stdout, os.Stderr
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

	api.GET("/openapi.json", openAPISpec(loadOpenAPISpec()))

	q := db.New(pool)
	ah := &AuthHandler{q: q, cfg: cfg}
	api.GET("/signup_status", ah.SignupStatus)
	api.POST("/signup", ah.Signup)

	oidcH := &OIDCHandler{q: q, auth: ah, controlURL: proxyCfg.controlURL}
	api.GET("/oidc/providers", oidcH.ListPublicProviders)
	api.GET("/oidc/:slug/start", oidcH.Start)
	api.GET("/oidc/:slug/callback", oidcH.Callback)
	api.POST("/signin", ah.Signin)
	api.POST("/token_refresh", ah.TokenRefresh)
	api.POST("/signout", ah.Signout)

	e.GET(proxyLoginPath, (&ProxyLoginHandler{q: q, cfg: cfg, proxy: proxyCfg}).Login)

	hub := NewHub()
	if pool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := q.SetAllClientsOffline(ctx); err != nil {
			log.Printf("could not reset client statuses at startup: %v", err)
		}
		if err := q.FailAllPendingVMs(ctx, "the control server restarted before the client reported the result"); err != nil {
			log.Printf("could not fail pending vms at startup: %v", err)
		}
		cancel()
	}

	blobs := loadBlobStore(context.Background())
	certs := newCertIssuer(q, blobs, hub, proxyCfg)

	tasks := newTaskRunner(q, hub, certs, blobs)
	if pool != nil {
		runnerCtx, stopRunner := context.WithCancel(context.Background())
		defer stopRunner()
		go tasks.run(runnerCtx)
	} else {
		log.Print("DATABASE_URL not set; scheduled tasks will not run")
	}

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
	admin.PUT("/osimages/:id", adminH.UpdateOSImage)
	admin.PUT("/osimages/:id/config", adminH.UpdateOSImageConfig)
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

	profileH := &ProfileHandler{q: q}
	me := api.Group("/me", userJWT(cfg, q))
	me.GET("", profileH.GetMe)
	me.PUT("", profileH.UpdateMe)

	pats := me.Group("/tokens", denyPAT)
	pats.GET("", profileH.ListTokens)
	pats.POST("", profileH.CreateToken)
	pats.POST("/:id/revoke", profileH.RevokeToken)
	pats.DELETE("/:id", profileH.DeleteToken)

	userH := &UserHandler{q: q, pool: pool, hub: hub, prod: cfg.prod, proxy: proxyCfg, blobs: adminH.blobs, ch: ch}
	vms := api.Group("/vms", userJWT(cfg, q))
	vms.GET("", userH.ListVMs)
	vms.POST("", userH.CreateVM)
	vms.GET("/quota", userH.GetQuota)
	vms.GET("/hosts", userH.ListHosts)
	vms.GET("/kernels", userH.ListKernels)
	vms.GET("/osimages", userH.ListOSImages)
	vms.GET("/osimages/mine", userH.ListMyOSImages)
	vms.POST("/osimages", userH.CreateOSImage)
	vms.DELETE("/osimages/:id", userH.DeleteOSImage)
	vms.GET("/:id", userH.GetVM)
	vms.DELETE("/:id", userH.DeleteVM)
	vms.POST("/:id/start", userH.StartVM)
	vms.POST("/:id/stop", userH.StopVM)
	vms.POST("/:id/destroy", userH.DestroyVM)
	vms.PUT("/:id/ports", userH.UpdatePorts)
	vms.PUT("/:id/user", userH.UpdateDefaultUser)
	vms.POST("/:id/console-token", userH.ConsoleToken)
	vms.POST("/:id/web-session", userH.WebSession)
	vms.POST("/:id/desktop-token", userH.DesktopToken)
	vms.GET("/:id/rdp-credentials", userH.RDPCredentials)
	vms.GET("/:id/rdp-file", userH.RDPFile)
	vms.POST("/:id/rdp-rotate", userH.RDPRotate)
	vms.GET("/:id/domain", userH.GetCustomDomain)
	vms.POST("/:id/domain", userH.SetCustomDomain)
	vms.POST("/:id/domain/verify", userH.VerifyCustomDomain)
	vms.DELETE("/:id/domain", userH.DeleteCustomDomain)
	vms.GET("/:id/targets", userH.ListTargets)
	vms.GET("/:id/denied", userH.ListDeniedEgress)
	vms.POST("/:id/targets", userH.CreateTarget)
	vms.POST("/:id/targets/resolve", userH.ResolveTargetHost)
	vms.PUT("/:id/targets/:target_id", userH.UpdateTarget)
	vms.DELETE("/:id/targets/:target_id", userH.DeleteTarget)

	clientH := &ClientHandler{q: q, pool: pool, hub: hub, proxy: proxyCfg, blobs: blobs}
	ag := api.Group("/client")
	ag.POST("/enroll", clientH.Enroll)
	ag.GET("/connect", clientH.Connect)

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
		body := map[string]any{
			"all":        apiOK && dbOK,
			"db":         dbOK,
			"clickhouse": chOK,
			"api":        apiOK,
		}
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
