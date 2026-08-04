package main

import (
  "context"
  "fmt"
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
    Usage: "start the Nuxt dev server and the Echo API server",
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

// runServe starts `bun run dev` (in ./web-client) as a child process and then runs
// the Echo server. air sits above this process: on a Go rebuild it sends an
// interrupt, our handler kills the whole Nuxt process group, and we exit so the
// port is free before air re-runs the rebuilt binary.
func runServe(host string, port int) error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

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

  nuxt := exec.Command("bun", "run", "dev")
  nuxt.Dir = "web-client"
  nuxt.Stdout, nuxt.Stderr = os.Stdout, os.Stderr
  // Own process group so we can signal Nuxt + its vite/node children together.
  nuxt.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

  if err := nuxt.Start(); err != nil {
    return fmt.Errorf("failed to start nuxt dev server (is `bun` on PATH?): %w", err)
  }
  log.Printf("nuxt dev server started (pid %d)", nuxt.Process.Pid)

  // On interrupt/SIGTERM (Ctrl-C or air's rebuild signal): tear down the whole
  // Nuxt process group, then exit. SIGKILL as a backstop if it lingers.
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

  return runEchoServer(host, port, pool, loadAuthConfig())
}

// runEchoServer starts the Echo API server: /api/v1/* is handled here, every
// other path is reverse-proxied to the Nuxt dev server.
func runEchoServer(host string, port int, pool *pgxpool.Pool, cfg authConfig) error {
  e := echo.New()

  e.Use(middleware.RequestLogger())
  e.Use(middleware.Recover())
  e.Use(proxyToNuxt(nuxtTarget))

  api := e.Group("/api/v1")
  api.GET("/health", func(c *echo.Context) error {
    return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
  })

  // Auth: password signup/signin, refresh-token rotation, and session teardown.
  q := db.New(pool)
  ah := &AuthHandler{q: q, cfg: cfg}
  api.POST("/signup", ah.Signup)
  api.POST("/signin", ah.Signin)
  api.POST("/token_refresh", ah.TokenRefresh)
  api.POST("/signout", ah.Signout)

  // The hub only knows about agents connected to *this* process, so any row
  // left 'online' by a previous run is stale.
  hub := NewHub()
  if pool != nil {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    if err := q.SetAllAgentsOffline(ctx); err != nil {
      log.Printf("could not reset agent statuses at startup: %v", err)
    }
    // Same reasoning for VMs: a pending row was waiting on a result frame that
    // belonged to a socket this process never had.
    if err := q.FailAllPendingVMs(ctx, "the control server restarted before the agent reported the result"); err != nil {
      log.Printf("could not fail pending vms at startup: %v", err)
    }
    cancel()
  }

  // Admin: user management + refresh-token session management, JWT + admin gated.
  adminH := &AdminHandler{q: q, cfg: cfg, hub: hub}
  admin := api.Group("/admin", adminJWT(cfg))
  admin.GET("/users", adminH.ListUsers)
  admin.POST("/users", adminH.CreateUser)
  admin.DELETE("/users/:id", adminH.DeleteUser)
  admin.GET("/tokens", adminH.ListTokens)
  admin.POST("/tokens/:id/blacklist", adminH.BlacklistToken)
  admin.DELETE("/tokens/:id", adminH.DeleteToken)
  admin.POST("/tokens/cleanup", adminH.CleanupTokens)
  admin.GET("/agent-keys", adminH.ListAgentKeys)
  admin.POST("/agent-keys", adminH.CreateAgentKey)
  admin.POST("/agent-keys/:id/revoke", adminH.RevokeAgentKey)
  admin.DELETE("/agent-keys/:id", adminH.DeleteAgentKey)
  admin.GET("/agents", adminH.ListAgents)
  admin.POST("/agents/:id/revoke", adminH.RevokeAgent)
  admin.DELETE("/agents/:id", adminH.DeleteAgent)
  admin.GET("/agents/:id/vms", adminH.ListAgentVMs)
  admin.POST("/agents/:id/vms", adminH.CreateVM)
  admin.GET("/vms", adminH.ListVMs)
  admin.GET("/vms/:id", adminH.GetVM)
  admin.POST("/vms/:id/start", adminH.StartVM)
  admin.POST("/vms/:id/stop", adminH.StopVM)
  admin.POST("/vms/:id/destroy", adminH.DestroyVM)
  admin.DELETE("/vms/:id", adminH.DeleteVM)

  // Agents: enrollment + the persistent socket the server pushes jobs down.
  // Authenticated by enrollment key / agent token, not by the user JWT.
  agentH := &AgentHandler{q: q, pool: pool, hub: hub}
  ag := api.Group("/agent")
  ag.POST("/enroll", agentH.Enroll)
  ag.GET("/connect", agentH.Connect)

  // /ht/ is a deeper healthcheck: it reports API liveness plus DB reachability.
  api.GET("/ht/", func(c *echo.Context) error {
    apiOK := true
    dbOK := false
    if pool != nil {
      ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
      defer cancel()
      dbOK = pool.Ping(ctx) == nil
    }
    return c.JSON(http.StatusOK, map[string]bool{
      "all": apiOK && dbOK,
      "db":  dbOK,
      "api": apiOK,
    })
  })

  addr := host + ":" + strconv.Itoa(port)
  if err := e.Start(addr); err != nil {
    e.Logger.Error("failed to start server", "error", err)
    return err
  }
  return nil
}

// proxyToNuxt proxies every request whose path does NOT start with /api/ to the
// Nuxt dev server. API requests fall through to the next handler.
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
      if strings.HasPrefix(c.Request().URL.Path, "/api/") {
        return next(c)
      }
      proxy.ServeHTTP(c.Response(), c.Request())
      return nil
    }
  }
}
