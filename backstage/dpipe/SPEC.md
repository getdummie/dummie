# dpipe — Implementation Spec (v1)

**Role:** the data plane. A long-lived process that owns network connections and
moves their bytes. It takes commands from the `proxy` over a Unix socket. It stays
byte-opaque for TCP/HTTP, and **terminates the two protocols that can't be
fd-passed once encrypted — SSH and TLS** — because a terminated encrypted session's
state lives in userspace and must therefore live in the process that isn't
redeployed. For those it asks the proxy for the routing/authorization decision.

**Language / target:** **Go 1.26.5**. Primary target Linux (`splice(2)` fast path);
must also build/run on macOS for local dev.

> Paired with **proxy-spec.md**. The **Wire Protocol v1** section MUST be
> byte-for-byte compatible across both. `internal/control`, `internal/xnet`, and
> `internal/httpsniff` are shared source.

---

## 1. Responsibilities

1. Accept **duplex** control connections from `proxy` over a Unix domain socket
   (proxy initiates most commands; dpipe initiates `resolve`).
2. proxy→dpipe commands:
   - **copy** — adopt two connected socket fds; copy bytes bidirectionally (TCP
     and plaintext HTTP; opaque after the proxy's setup).
   - **ssh_accept** — adopt one *raw* (pre-SSH) TCP fd; run the SSH server;
     authorize+route via `resolve`; dial the target VM as SSH client; relay SSH
     channels.
   - **tls_accept** — adopt one *raw* (pre-TLS) TCP fd; terminate TLS; sniff the
     HTTP `Host`; route via `resolve`; dial the backend; copy the decrypted stream.
   - **listen_forward** / **stop** / **status** — as before.
3. dpipe→proxy command:
   - **resolve** — ask the proxy to authorize/route a connection. Two kinds:
     `ssh` (`{ssh_user, ssh_pubkey, ssh_fp, client_ip}` → `{authorized, target,
     remote_user}`) and `http` (`{host, sni, client_ip}` → `{authorized, target}`).
4. **Self-upgrade:** a new dpipe adopts all *listening* sockets; the old keeps
   its open connections (copies, SSH relays, TLS sessions) running to completion,
   then exits.

Non-goals (v1): HTTP/2 / ALPN `h2` (force HTTP/1.1 over TLS), mTLS / client-cert
auth, ACME / automatic certificates, HTTP body parsing, moving established
connections between dpipe instances, SSH cert/agent-forwarding special handling.

---

## 2. Process model & lifecycle

- **Fresh start** (`dpipe -config dpipe.yaml`): remove stale non-connectable
  socket files; bind+listen `control_socket`/`upgrade_socket` (`unix` stream, mode
  `0600`, `SetUnlinkOnClose(false)`); load SSH keys if `ssh.enabled` and TLS certs
  if `tls.enabled`; serve.
- **Upgrade start** (`-upgrade`): connect to `upgrade_socket`; handover handshake
  (§7); adopt control/upgrade/listen_forward listeners; serve; `handover_ack`. No
  running instance → exit non-zero.
- **Signals** `SIGTERM`/`SIGINT`: stop accepting; drain to zero active connections
  (honor `drain_timeout`, 0 = forever); exit 0.

---

## 3. Configuration

```yaml
control_socket: /run/dpipe/control.sock
upgrade_socket: /run/dpipe/upgrade.sock
drain_timeout: 0s
log_level: info

ssh:
  enabled: true
  host_key: ./keys/dpipe_host_ed25519
  client_key: ./keys/dpipe_client_ed25519
  backend_known_hosts: ./keys/known_hosts     # empty => insecure (dev only) + warn
  dial_timeout: 5s
  resolve_timeout: 3s

tls:
  enabled: true
  certs:                                       # selected by SNI
    - sni: vm1.local
      cert: ./keys/vm1.crt
      key:  ./keys/vm1.key
    - sni: vm2.local
      cert: ./keys/vm2.crt
      key:  ./keys/vm2.key
  default_cert: ./keys/default.crt             # optional fallback when SNI misses
  default_key:  ./keys/default.key
  min_version: "1.2"                           # 1.2 or 1.3
  dial_timeout: 5s
  resolve_timeout: 3s
  sniff_timeout: 5s
  sniff_max_bytes: 65536
```

Validation: socket paths required; if `ssh.enabled`, `host_key`+`client_key` must
load; if `tls.enabled`, at least one `certs[]` or a `default_cert`/`default_key`
pair must load. Empty `backend_known_hosts` → insecure host-key callback + startup
warning (dev only). `min_version` maps to `tls.VersionTLS12/13`.

**Dependencies** (verify approved list first): `golang.org/x/sys/unix`,
`golang.org/x/crypto/ssh`, `gopkg.in/yaml.v3`. TLS uses the standard library
`crypto/tls` — no extra dependency.

---

## 4. Repository layout (shared with proxy)
```
/cmd/dpipe/main.go
/internal/control/       # SHARED: protocol.go (Msg+JSON), conn.go (SendMsg/RecvMsg + SCM_RIGHTS)
/internal/httpsniff/     # SHARED: ReadHeaderBlock + ParseHost (used by proxy plaintext & dpipe post-TLS)
/internal/dpipe/
    server.go            # duplex control loop
    copy.go              # bidirectional byte copy + half-close
    listenforward.go
    sshterm.go           # SSH server termination + client dial
    sshrelay.go          # SSH channel/request relay
    tlsterm.go           # TLS termination + Host sniff + backend copy (NEW)
    registry.go          # active conns (copies + ssh + tls + forwards), drain
    upgrade.go
    config.go
/internal/xnet/          # SHARED: FileConn, FileListener, ListenerFD, Listen(SO_REUSEPORT)
```

---

## 5. Wire Protocol v1  *(identical in proxy-spec.md)*

**Transport** `AF_UNIX`/`SOCK_STREAM`/`"unix"`. **Framing** one message per
`sendmsg(2)`/`recvmsg(2)`, JSON ≤4096B, fds via one `SCM_RIGHTS` (`MAXFDS=2`).
**Duplex:** both sides may initiate requests; each side's reader routes
`ok`/`error`/`resolved` to waiters by `id` and dispatches inbound requests.

### Requests
| type             | direction        | extra fields                                     | fds                       |
|------------------|------------------|--------------------------------------------------|---------------------------|
| `copy`           | proxy → dpipe    | `protocol:"tcp"\|"http"`                          | `[client_fd, backend_fd]` |
| `ssh_accept`     | proxy → dpipe    | `protocol:"ssh"`                                  | `[client_fd]` (raw TCP)   |
| `tls_accept`     | proxy → dpipe    | `protocol:"tls"`                                  | `[client_fd]` (raw TCP)   |
| `listen_forward` | proxy → dpipe    | `listen`, `target`                                | none                      |
| `stop`           | proxy → dpipe    | targets a listen_forward `id`                     | none                      |
| `status`         | proxy → dpipe    | —                                                 | none                      |
| `resolve`        | dpipe → proxy    | `kind:"ssh"\|"http"` + kind fields (below)        | none                      |

`resolve` kind fields — `ssh`: `ssh_user, ssh_pubkey, ssh_fp, client_ip`;
`http`: `host, sni, client_ip`.

### Replies (correlated by `id`)
| type       | fields                                                                        |
|------------|-------------------------------------------------------------------------------|
| `ok`       | `status`: `active_conns`, `listen_forwards`, `draining`                         |
| `error`    | `error`                                                                        |
| `resolved` | `authorized:bool`; if true `target:"host:port"`, and `remote_user` (ssh only)  |

### Self-upgrade (over `upgrade_socket`)
`handover_request` (new→old); `handover` (old→new) with
`sockets:[{kind:"control"|"upgrade"|"listen_forward", id, listen, target}]` + one
listener fd per entry in order; `handover_ack` (new→old).

### Shared `Msg` (`internal/control`)
```go
type Msg struct {
    V    int    `json:"v"`
    Type string `json:"type"`
    ID   string `json:"id,omitempty"`
    Protocol string `json:"protocol,omitempty"`   // copy / ssh_accept / tls_accept
    Listen   string `json:"listen,omitempty"`
    Target   string `json:"target,omitempty"`     // listen_forward / resolved
    Kind string `json:"kind,omitempty"`            // resolve: "ssh" | "http"
    SSHUser        string `json:"ssh_user,omitempty"`
    SSHPubKey      string `json:"ssh_pubkey,omitempty"`
    SSHFingerprint string `json:"ssh_fp,omitempty"`
    Host string `json:"host,omitempty"`            // resolve http
    SNI  string `json:"sni,omitempty"`             // resolve http
    ClientIP string `json:"client_ip,omitempty"`
    Authorized bool   `json:"authorized,omitempty"`
    RemoteUser string `json:"remote_user,omitempty"`
    Error string `json:"error,omitempty"`
    ActiveConns    int  `json:"active_conns,omitempty"`
    ListenForwards int  `json:"listen_forwards,omitempty"`
    Draining       bool `json:"draining,omitempty"`
    Sockets []SockDesc `json:"sockets,omitempty"`
}
type SockDesc struct{ Kind, ID, Listen, Target string }
func SendMsg(c *net.UnixConn, m Msg, fds []int) error
func RecvMsg(c *net.UnixConn) (m Msg, fds []int, err error)
```

---

## 6. Command handlers

### 6.1 `copy` (TCP/HTTP)
```
on copy(fds=[clientFD, backendFD]):
    if draining: error "draining"; close both; return
    client := xnet.FileConn(clientFD); backend := xnet.FileConn(backendFD)
    registry.Add(id); reply ok(id)
    go { Pipe(client, backend); registry.Done(id) }
```

### 6.2 `Pipe` — bidirectional copy with half-close (`copy.go`)
```
Pipe(a,b): copyHalf(a,b) ‖ copyHalf(b,a); wait; a.Close(); b.Close()
copyHalf(dst,src): io.Copy(dst,src); if cw,ok:=dst.(interface{CloseWrite()error});ok{cw.CloseWrite()}
```
Linux `io.Copy(TCP,TCP)` uses `splice(2)`. For TLS one side is a `*tls.Conn`
(userspace copy — expected).

### 6.3 `ssh_accept` — SSH termination (`sshterm.go` + `sshrelay.go`)
Unchanged from the SSH-termination design: adopt the raw fd, `ssh.NewServerConn`
with a `PublicKeyCallback` that issues `resolve{kind:"ssh"}` to the proxy, dial the
returned `target` as an SSH client using `ssh.client_key`, then relay channels and
requests both directions (global requests, client- and vm-opened channels, data +
stderr with half-close, channel requests incl. exit-status). Verify key ownership
via the library's signature check; the proxy authorizes which VM the key may reach.
(See the SSH sections — behavior is identical.)

### 6.4 `tls_accept` — TLS termination (`tlsterm.go`, NEW)
Rationale identical to SSH: a live TLS session can't be fd-passed, so dpipe owns
it end to end; the proxy hands over the raw pre-TLS socket.
```
on tls_accept(fds=[clientFD]):
    if draining or !tls.enabled: close(clientFD); return
    client := xnet.FileConn(clientFD)
    go serveTLS(client)

func serveTLS(client net.Conn):
    remoteIP := host(client.RemoteAddr())
    cfg := &tls.Config{
        MinVersion:   tlsMin,                 // from config
        NextProtos:   []string{"http/1.1"},   // force HTTP/1.1; no h2 in v1
        GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
            return certForSNI(chi.ServerName)  // config certs; default_cert fallback; error if none
        },
    }
    tc := tls.Server(client, cfg)
    if err := tc.HandshakeContext(ctx(tls.dial_timeout)); err != nil { client.Close(); return }
    sni := tc.ConnectionState().ServerName

    tc.SetReadDeadline(now+tls.sniff_timeout)
    buf, host, err := httpsniff.ReadHeaderBlock(tc, tls.sniff_max_bytes)   // decrypted stream
    tc.SetReadDeadline(zero)
    routeHost := firstNonEmpty(host, sni)
    if err != nil || routeHost == "" { writeQuickTLS(tc,400); tc.Close(); return }

    rep, err := ctrl.Resolve(ctx(tls.resolve_timeout), Msg{
        Type:"resolve", ID:uuid(), Kind:"http", Host:routeHost, SNI:sni, ClientIP:remoteIP})
    if err != nil || !rep.Authorized { writeQuickTLS(tc,502); tc.Close(); return }

    backend, err := net.DialTimeout("tcp", rep.Target, tls.dial_timeout)
    if err != nil { writeQuickTLS(tc,502); tc.Close(); return }
    if _, err := backend.Write(buf); err != nil { tc.Close(); backend.Close(); return } // replay decrypted prefix

    registry.Add(id)
    Pipe(tc, backend)          // decrypted client stream <-> plaintext HTTP/1.1 backend
    registry.Done(id)
```
`certForSNI` builds a `map[string]*tls.Certificate` at startup (exact SNI match,
then `default_*`); missing → error (handshake fails cleanly). `writeQuickTLS`
writes a minimal `HTTP/1.1 <code>` response over the TLS conn.

### 6.5 `listen_forward` / `stop` / `status`
As before; `status` returns `registry.Snapshot()`.

---

## 7. Self-upgrade
Same as the opaque design; TLS sessions and SSH relays are ordinary "active
connections" that drain in the old process. Old side after `handover_ack`: set
`draining`; close listeners with `SetUnlinkOnClose(false)`; close proxy control
connections (so proxies reconnect to the new instance — carrying future
`resolve`s); keep active conns running; exit 0 at `Active()==0` or timeout.

---

## 8. Registry
`Add/Done/Active` count copies, SSH relays, TLS sessions, and forwarded conns
identically. `Active()==0` while draining → exit.

---

## 9. `xnet` / `httpsniff` (shared)
`xnet`: `FileConn`, `FileListener`, `ListenerFD` (via `SyscallConn().Control`),
`Listen` (SO_REUSEPORT). `httpsniff`: `ReadHeaderBlock(r, max) ([]byte, host string, err)`
(reads to `\r\n\r\n`, never loses over-read body bytes) + `ParseHost`.

---

## 10. Error handling
- Bad control message / oversize / wrong fd count → `error` if possible; close that
  control connection; never crash.
- `resolve` failure/timeout: SSH → reject auth (retryable); TLS → `502` over the
  TLS conn then close. Existing sessions unaffected.
- Backend dial/handshake failure → close cleanly.
- Proxy crash → control conns close (normal); active conns continue.

---

## 11. Observability & security
- `log/slog` with per-connection `id`; log accept, resolve decision (host/SNI or
  user/fingerprint + target — never key material beyond fingerprint, never payload),
  session start/end.
- **Key/cert material** (`ssh.client_key`, `ssh.host_key`, all `tls` keys) stored
  `0600`; dpipe least-privileged. `client_key` is powerful (reaches every
  configured VM) — v2 should move to per-target keys / short-lived certs.
- TLS: enforce `min_version` ≥ 1.2; force HTTP/1.1 (no h2) in v1; no mTLS.
- Backend SSH host-key: empty `backend_known_hosts` uses an insecure callback —
  dev/localhost only, warn at startup; production pins `known_hosts`.

---

## 12. Testing (acceptance criteria)
**Unit:** control encode/decode; `SendMsg`/`RecvMsg` fd round-trip over `socketpair`;
`Pipe` half-close; `httpsniff.ReadHeaderBlock` (header-only; header+partial-body no
loss; absolute-form URI; missing Host; oversize).
**Integration (Linux + macOS):**
1. `copy`: echo backend, bytes both ways.
2. `listen_forward` + `stop`.
3. **SSH termination (must pass):** real `ssh` → proxy → dpipe terminates →
   local `sshd`: interactive shell; `exec` exit status; `scp`/`sftp`; `-L` forward;
   unauthorized key rejected; authorized key → correct target.
4. **TLS termination (must pass):** real `curl https://…` (with `--resolve` to the
   proxy and a trusted demo CA) for `vm1.local`/`vm2.local`: assert correct backend,
   correct SNI cert served, request+response body intact, unknown host → 502,
   HTTP/1.1 enforced (no h2).
5. **dpipe self-upgrade (must pass):** with a long `copy`, a live SSH session,
   and a slow HTTPS transfer all in flight, run `-upgrade`; assert all survive to
   completion in the old process, new work is served by the new process, old exits
   0, socket files remain.

`justfile` (`build`, `test`, `race`, `lint`, `demo`, `keys`) and
`scripts/demo.sh` + `scripts/keys.sh` (generate SSH host/client/user keys, a demo
CA + per-SNI certs, and a `known_hosts`).

---

## 13. Out of scope (v2+)
HTTP/2 / ALPN, mTLS / client-cert auth, ACME / auto-certs, per-owner-IP SSH
routing, SSH cert/agent-forwarding, moving established connections during upgrade,
metrics/pprof, pushed routing tables (to avoid per-connection `resolve`).
