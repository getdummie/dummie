package main

import (
  "bufio"
  "bytes"
  "context"
  "encoding/json"
  "fmt"
  "io"
  "net"
  "net/http"
  "os"
  "strings"
  "time"
)

// client is the CLI's half of the socket. Everything it does is something the
// daemon does on its behalf as root, so the CLI itself needs no privileges
// beyond membership of the dagent group.
type client struct {
  socket string
}

// newClient returns nil when there is no usable daemon, which is the signal for
// the caller to fall back to doing the work in-process.
func newClient(socket string) *client {
  if !socketUsable(socket) {
    return nil
  }
  return &client{socket: socket}
}

func (c *client) http() *http.Client {
  return &http.Client{
    Transport: &http.Transport{
      DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return (&net.Dialer{}).DialContext(ctx, "unix", c.socket)
      },
    },
    // Creating a VM downloads images and builds a filesystem; there is no
    // sensible ceiling, and progress arrives as a stream anyway.
    Timeout: 0,
  }
}

func (c *client) do(ctx context.Context, method, path string, body, out any) error {
  var rdr io.Reader
  if body != nil {
    b, err := json.Marshal(body)
    if err != nil {
      return err
    }
    rdr = bytes.NewReader(b)
  }
  req, err := http.NewRequestWithContext(ctx, method, "http://dagent"+path, rdr)
  if err != nil {
    return err
  }
  if body != nil {
    req.Header.Set("Content-Type", "application/json")
  }

  res, err := c.http().Do(req)
  if err != nil {
    return fmt.Errorf("could not reach the dagent daemon: %w", err)
  }
  defer res.Body.Close()

  if res.StatusCode >= 400 {
    var e errorBody
    if err := json.NewDecoder(res.Body).Decode(&e); err == nil && e.Error != "" {
      return fmt.Errorf("%s", e.Error)
    }
    return fmt.Errorf("daemon returned %s", res.Status)
  }
  if out == nil {
    return nil
  }
  return json.NewDecoder(res.Body).Decode(out)
}

// create streams the daemon's progress to w as it arrives, so a long download
// looks the same as it does when run directly.
func (c *client) create(ctx context.Context, req createRequest, w io.Writer) (vm, error) {
  b, err := json.Marshal(req)
  if err != nil {
    return vm{}, err
  }
  return c.streamVM(ctx, "http://dagent/v1/vms", b, w)
}

// start boots an existing VM. It is the same NDJSON exchange as create -- both
// end in either a VM or an error after an unknown number of progress lines --
// so it is the same reader.
func (c *client) start(ctx context.Context, ref string, w io.Writer) (vm, error) {
  return c.streamVM(ctx, "http://dagent/v1/vms/"+ref+"/start", nil, w)
}

// streamVM posts and then consumes the daemon's NDJSON progress stream, printing
// log lines to w until the terminating VM or error arrives.
func (c *client) streamVM(ctx context.Context, url string, body []byte, w io.Writer) (vm, error) {
  var reader io.Reader
  if body != nil {
    reader = bytes.NewReader(body)
  }
  hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reader)
  if err != nil {
    return vm{}, err
  }
  if body != nil {
    hreq.Header.Set("Content-Type", "application/json")
  }

  res, err := c.http().Do(hreq)
  if err != nil {
    return vm{}, fmt.Errorf("could not reach the dagent daemon: %w", err)
  }
  defer res.Body.Close()

  if res.StatusCode >= 400 {
    var e errorBody
    _ = json.NewDecoder(res.Body).Decode(&e)
    if e.Error != "" {
      return vm{}, fmt.Errorf("%s", e.Error)
    }
    return vm{}, fmt.Errorf("daemon returned %s", res.Status)
  }

  dec := json.NewDecoder(res.Body)
  for {
    var ev event
    if err := dec.Decode(&ev); err != nil {
      if err == io.EOF {
        return vm{}, fmt.Errorf("the daemon closed the connection without finishing")
      }
      return vm{}, err
    }
    switch {
    case ev.Error != "":
      return vm{}, fmt.Errorf("%s", ev.Error)
    case ev.VM != nil:
      return *ev.VM, nil
    case ev.Log != "":
      fmt.Fprintln(w, ev.Log)
    }
  }
}

func (c *client) list(ctx context.Context) ([]vmStatus, error) {
  var out []vmStatus
  return out, c.do(ctx, http.MethodGet, "/v1/vms", nil, &out)
}

func (c *client) stop(ctx context.Context, ref string, timeout time.Duration) error {
  path := fmt.Sprintf("/v1/vms/%s/stop?timeout=%s", ref, timeout)
  return c.do(ctx, http.MethodPost, path, nil, nil)
}

func (c *client) remove(ctx context.Context, ref string) error {
  return c.do(ctx, http.MethodDelete, "/v1/vms/"+ref, nil, nil)
}

// console does the HTTP handshake by hand and then hands the raw socket to the
// same terminal proxy the direct path uses. net/http's client cannot give back
// a connection to read and write arbitrarily, and the exchange is simple enough
// that writing the request line is easier than working around that.
func (c *client) console(ctx context.Context, ref string) error {
  conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socket)
  if err != nil {
    return fmt.Errorf("could not reach the dagent daemon: %w", err)
  }
  defer conn.Close()

  req := fmt.Sprintf("GET /v1/vms/%s/console HTTP/1.1\r\nHost: dagent\r\n"+
    "Connection: Upgrade\r\nUpgrade: dagent-console\r\n\r\n", ref)
  if _, err := io.WriteString(conn, req); err != nil {
    return err
  }

  br := bufio.NewReader(conn)
  status, err := br.ReadString('\n')
  if err != nil {
    return err
  }
  if !strings.Contains(status, "101") {
    // Read whatever explanation came with it rather than reporting the code.
    rest, _ := io.ReadAll(br)
    var e errorBody
    if i := bytes.IndexByte(rest, '{'); i >= 0 {
      _ = json.Unmarshal(rest[i:], &e)
    }
    if e.Error != "" {
      return fmt.Errorf("%s", e.Error)
    }
    return fmt.Errorf("console request failed: %s", strings.TrimSpace(status))
  }
  // Drain the remaining headers.
  for {
    line, err := br.ReadString('\n')
    if err != nil {
      return err
    }
    if strings.TrimSpace(line) == "" {
      break
    }
  }

  return proxyConsole(conn, br)
}

// --- shared dispatch --------------------------------------------------------

// dispatch decides between the daemon and doing the work here. Falling back
// keeps the direct path usable for development and when the daemon is broken,
// without making it the normal case.
func dispatch(socket string) (*client, error) {
  if c := newClient(socket); c != nil {
    return c, nil
  }
  if os.Geteuid() == 0 {
    return nil, nil // run in-process
  }
  return nil, fmt.Errorf("no dagent daemon at %s, and this is not running as root;\n"+
    "  run `sudo dagent install` once, then add yourself: sudo usermod -aG %s $USER",
    socket, defaultGroup)
}
