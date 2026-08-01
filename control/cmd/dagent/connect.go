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
)

// errTerminal marks a failure that retrying cannot fix -- a revoked or unknown
// credential. A revoked agent should stop, not hammer the control plane.
var errTerminal = errors.New("terminal error")

func connectCommand() *cli.Command {
  return &cli.Command{
    Name:  "connect",
    Usage: "enroll if needed, then hold a persistent connection to the control server",
    Flags: []cli.Flag{
      &cli.StringFlag{
        Name:     "control-url",
        Usage:    "base URL of the control server, e.g. https://control.example.com",
        Sources:  cli.EnvVars("DAGENT_CONTROL_URL"),
        Required: true,
      },
      &cli.StringFlag{
        Name:    "key",
        Usage:   "enrollment key; only needed the first time on a machine",
        Sources: cli.EnvVars("DAGENT_KEY"),
      },
      &cli.BoolFlag{
        Name:    "insecure",
        Usage:   "allow a plain-http control URL and skip TLS certificate verification",
        Sources: cli.EnvVars("DAGENT_INSECURE"),
      },
      &cli.StringFlag{
        Name:    "state-dir",
        Usage:   "directory holding agent.json (default /etc/dagent, or the user config dir)",
        Sources: cli.EnvVars("DAGENT_STATE_DIR"),
      },
    },
    Action: func(ctx context.Context, cmd *cli.Command) error {
      return runConnect(cmd.String("control-url"), cmd.String("key"),
        cmd.String("state-dir"), cmd.Bool("insecure"))
    },
  }
}

func runConnect(controlURL, key, stateDir string, insecure bool) error {
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
  st, err := loadState(stateDir)
  if err != nil {
    return fmt.Errorf("could not read agent state in %s: %w", stateDir, err)
  }

  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  client := httpClient(insecure)

  // Enrolling on every start would burn a use of a use-limited key each time
  // the agent restarts, so saved credentials always win over --key.
  if st.Token == "" {
    if key == "" {
      return fmt.Errorf("no agent token in %s; pass --key to enroll this machine", stateDir)
    }
    st, err = enroll(ctx, client, base, key)
    if err != nil {
      return err
    }
    if err := saveState(stateDir, st); err != nil {
      return fmt.Errorf("enrolled but could not save state to %s: %w", stateDir, err)
    }
    log.Printf("enrolled as agent %s", st.AgentID)
  } else if key != "" {
    log.Print("already enrolled; ignoring --key")
  }

  return connectLoop(ctx, client, base, st)
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
  return state{AgentID: out.AgentID, Token: out.Token, ControlURL: base.String()}, nil
}

func hostname() string {
  h, err := os.Hostname()
  if err != nil {
    return ""
  }
  return h
}

// --- connection loop --------------------------------------------------------

func connectLoop(ctx context.Context, client *http.Client, base *url.URL, st state) error {
  backoff := backoffMin

  for {
    start := time.Now()
    err := connectOnce(ctx, client, base, st)

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

func connectOnce(ctx context.Context, client *http.Client, base *url.URL, st state) error {
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
      return fmt.Errorf("%w: agent token rejected; this agent has been revoked or deleted", errTerminal)
    }
    return err
  }
  defer ws.CloseNow()

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
  log.Printf("connected to %s as agent %s", base.Host, st.AgentID)

  return readLoop(ctx, ws)
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
  })
  if err != nil {
    return err
  }
  return writeEnvelope(ctx, ws, env)
}

// readLoop blocks until the socket dies. There are no job types yet, so
// anything unrecognised is logged and ignored -- handling a new type later is
// an added case here, nothing more.
func readLoop(ctx context.Context, ws *websocket.Conn) error {
  for {
    env, err := readEnvelope(ctx, ws)
    if err != nil {
      if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
        return nil
      }
      return err
    }

    switch env.Type {
    case proto.TypeJob:
      log.Printf("received job %s (no handler yet): %s", env.ID, env.Payload)
    case proto.TypeError:
      var p proto.ErrorPayload
      _ = json.Unmarshal(env.Payload, &p)
      log.Printf("server reported an error: %s", p.Message)
    default:
      log.Printf("ignoring unexpected frame type %q", env.Type)
    }
  }
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
