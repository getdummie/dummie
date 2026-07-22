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

  "github.com/labstack/echo/v5"
  "github.com/labstack/echo/v5/middleware"
  "github.com/urfave/cli/v3"
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

// runServe starts `bun run dev` (in ./client) as a child process and then runs
// the Echo server. air sits above this process: on a Go rebuild it sends an
// interrupt, our handler kills the whole Nuxt process group, and we exit so the
// port is free before air re-runs the rebuilt binary.
func runServe(host string, port int) error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  nuxt := exec.Command("bun", "run", "dev")
  nuxt.Dir = "client"
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

  return runEchoServer(host, port)
}

// runEchoServer starts the Echo API server: /api/v1/* is handled here, every
// other path is reverse-proxied to the Nuxt dev server.
func runEchoServer(host string, port int) error {
  e := echo.New()

  e.Use(middleware.RequestLogger())
  e.Use(middleware.Recover())
  e.Use(proxyToNuxt(nuxtTarget))

  api := e.Group("/api/v1")
  api.GET("/health", func(c *echo.Context) error {
    return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
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
