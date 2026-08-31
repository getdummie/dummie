# dproxy — Implementation Spec (v1)

**Role:** the control plane / "brains." It owns the public ingress listeners,
decides where each connection goes, and hands connections to `dpipe` so it holds
no long-lived connection state and can be redeployed at any time.

Per protocol:
- **HTTP** (plaintext) — route per-connection by first request's `Host`; dial
  backend; hand off (`copy`).
- **TCP** (opaque) — route by ingress listener; dial backend; hand off (`copy`).
- **HTTPS/TLS** — accept the raw TCP and hand it to dpipe (`tls_accept`) *before*
  any TLS bytes; dpipe terminates TLS, sniffs `Host`, and calls back with
  `resolve{kind:"http"}`, which dproxy answers from its host map. TLS is
  terminated in dpipe (not dproxy) for the same reason as SSH: a live TLS
  session can't be fd-passed, so it must live in the process that isn't redeployed.
- **SSH** — accept raw TCP and hand off (`ssh_accept`); dpipe terminates SSH and
  calls back with `resolve{kind:"ssh"}`.

dproxy never sees TLS/SSH payload; it only makes routing/authorization
decisions.

**Language / target:** **Go 1.26.5**; Linux primary, macOS for local dev.

> Paired with **dpipe-spec.md**. The **Wire Protocol v1** section MUST match it
> exactly. `internal/control`, `internal/xnet`, `internal/httpsniff` are shared.

---

## 1. Responsibilities
1. Serve ingress: HTTP (shared listener, Host routing), TCP (per-route listener),
   HTTPS (shared listener, raw accept → `tls_accept`), SSH (shared listener, raw
   accept → `ssh_accept`).
2. HTTP/TCP: dial backend, (HTTP) replay sniffed prefix, `copy` hand-off.
3. HTTPS/SSH: hand the raw pre-crypto socket to dpipe; no backend dial here (the
   backend is chosen after termination, via `resolve`).
4. Answer `resolve` from dpipe: `kind:"http"` (host→backend from the host map) and
   `kind:"ssh"` ({pubkey, login name}→{backend, remote_user} from the users policy).
5. Maintain a resilient **duplex** control connection to dpipe.
6. Optionally program `listen_forward` jobs at startup.

Non-goals (v1): terminating TLS/SSH itself, HTTP/2, mTLS, ACME, per-request HTTP
routing.

---

## 2. Process model & lifecycle
- **Start:** load config; connect `control_socket` (retry w/ backoff); submit
  `listen_forward`s; bind all ingress listeners with **SO_REUSEPORT**; serve.
- **Redeploy:** new dproxy binds same ports (SO_REUSEPORT) → no gap; `SIGTERM` old →
  stop accepting, exit immediately. All handed-off TCP/HTTP copies and all
  terminated TLS/SSH sessions live in dpipe and are unaffected.
- **Signals:** `SIGTERM`/`SIGINT` → stop accepting, close control conn, exit 0.

Redeploy-safety = (a) all long-lived connections live in dpipe; (b) SO_REUSEPORT
overlap covers new-connection continuity. For a *new* HTTPS/SSH connection the only
dproxy dependency is the brief `resolve` round trip during termination; if dproxy
is momentarily down then, that one connection retries — established ones are
untouched.

---

## 3. Configuration
```yaml
control_socket: /run/dpipe/control.sock

http:                       # plaintext HTTP (dproxy sniffs + hands off copy)
  listen: ":8080"
  reuseport: true
  hosts:
    one.vm.local:
      host: 127.0.0.1
      unauthenticated_ports: [8001]   # ports reachable without auth
      default_port: 8001
      remote_user: appuser            # console lands here; empty => console.remote_user
    two.vm.local:
      host: 127.0.0.1
      unauthenticated_ports: []       # empty => every port needs auth
      default_port: 8002
  default: ""               # host:port; empty => 502

https:                      # TLS ingress (dproxy accepts raw, dpipe terminates)
  listen: ":8443"
  reuseport: true
  # routing reuses http.hosts via resolve{kind:"http"}; certs live in dpipe.tls

site:                       # the fleet's own page, on hostnames that are not guests
  listen: "127.0.0.1:8079"  # loopback backend, so both ingresses reach it
  html_file: /etc/dclient/dproxy-site.html
  hosts:
    - www.vm.local          # added to the routing table, never over a published VM

tcp:
  - listen: ":9000"
    target: "127.0.0.1:9000"
    protocol: tcp

ssh:
  listen: ":2222"
  reuseport: true
  users:
    - pubkey_file: ./keys/alice.pub
      vm_name: "build"
      target: "127.0.0.1:22"
      remote_user: "dev"
    - pubkey: "ssh-ed25519 AAAA... bob@laptop"
      vm_name: "test"
      target: "127.0.0.1:2200"
      remote_user: "root"

acme:
  challenge_target: "127.0.0.1:8078"

listen_forwards:
  - listen: "127.0.0.1:15432"
    target: "127.0.0.1:5432"

dial_timeout: 5s
http_sniff_timeout: 5s
http_sniff_max_bytes: 65536
log_level: info
```
Validation: `control_socket` required; ≥1 ingress; unique listeners; each
`ssh.users[]` has exactly one of `pubkey`/`pubkey_file` + `vm_name` (usable as an
ssh login name) + `target` (`host:port`) + `remote_user`. The same
{`pubkey`, `vm_name`} twice is rejected; the same `pubkey` under different
`vm_name`s is the normal case, since one user owns one key and may own many VMs.
If `https` is set, `dpipe.tls.enabled` must be true (document the
cross-process dependency). `site` requires an `http` ingress, an `html_file` that
exists at startup, and ≥1 host; its listener is claimed like any other, so it
cannot collide with an ingress. `acme` requires an `http` ingress and a
`challenge_target` in `host:port` form; it claims no listener of its own, since
it is somewhere to dial rather than somewhere to accept.

**Dependencies** (verify approved list first): `golang.org/x/sys/unix`,
`golang.org/x/crypto/ssh` (only to parse/normalize pubkeys for the policy map — the
dproxy does no SSH/TLS I/O), `gopkg.in/yaml.v3`.

---

## 4. Repository layout (shared with dpipe)
```
/cmd/dproxy/main.go
/internal/control/       # SHARED (canonical in dpipe-spec.md)
/internal/httpsniff/     # SHARED
/internal/proxy/
    proxy.go             # wiring, listeners, signals
    router.go            # http host map + tcp routes
    handoff.go           # dial + fd extraction + copy / ssh_accept / tls_accept
    resolver.go          # answer resolve (kind http + ssh)
    client.go            # duplex control connection (+ reconnect, request handling)
    config.go
/internal/xnet/          # SHARED
```

---

## 5. Wire Protocol v1  *(identical in dpipe-spec.md — keep in sync)*
**Transport** `AF_UNIX`/`SOCK_STREAM`/`"unix"`. **Framing** one message per
`sendmsg`/`recvmsg`, JSON ≤4096B, fds via one `SCM_RIGHTS` (`MAXFDS=2`).
**Duplex:** both sides may initiate; readers route `ok`/`error`/`resolved` by `id`
and dispatch inbound requests.

### Requests
| type             | direction        | extra fields                                | fds                       |
|------------------|------------------|---------------------------------------------|---------------------------|
| `copy`           | dproxy → dpipe    | `protocol:"tcp"\|"http"`                     | `[client_fd, backend_fd]` |
| `ssh_accept`     | dproxy → dpipe    | `protocol:"ssh"`                             | `[client_fd]` (raw TCP)   |
| `tls_accept`     | dproxy → dpipe    | `protocol:"tls"`                             | `[client_fd]` (raw TCP)   |
| `listen_forward` | dproxy → dpipe    | `listen`, `target`                           | none                      |
| `stop`           | dproxy → dpipe    | targets a listen_forward `id`                | none                      |
| `status`         | dproxy → dpipe    | —                                            | none                      |
| `resolve`        | dpipe → dproxy    | `kind:"ssh"\|"http"` + kind fields           | none                      |

`resolve` kind fields — `ssh`: `ssh_user, ssh_pubkey, ssh_fp, client_ip`;
`http`: `host, sni, client_ip`, plus the request details the auth policy reads —
`cookie` (the `Cookie` header verbatim, ≤ `MaxResolveCookie`), `path` (request
target, ≤ `MaxResolvePath`), `accept`, `upgrade`. A field that does not fit is
omitted, which reads as "absent" and fails closed.

### Replies (by `id`)
| type       | fields                                                                       |
|------------|------------------------------------------------------------------------------|
| `ok`       | `status`: `active_conns`, `listen_forwards`, `draining`                        |
| `error`    | `error`                                                                       |
| `resolved` | `authorized`; if true `target`, and `remote_user` (ssh only); on an `ssh` resolve whose key is known but whose login name named none of its VMs, `notice` (≤ `MaxNotice`) — the text dpipe prints before hanging up; if not authorized on an `http` resolve, the response dpipe must write: `status`, and `location`/`set_cookie` for a 302; a console hostname replies `protocol:"console"` with `target`, `remote_user`, `sub`, `ws_key` |

`Msg` struct: identical to dpipe-spec §5 (shared `internal/control`).

---

## 6. Duplex control client (`client.go`)
One persistent connection; multiplex both directions.
- **Reconnect** on error/EOF w/ capped backoff (a drop = dpipe self-upgraded;
  reconnect lands on the new instance).
- **Reader:** `RecvMsg`; `ok`/`error`/`resolved` → waiter by `id`; `resolve` →
  `resolver.Handle` then `SendMsg` a `resolved` reply.
- **Writer:** mutex-guarded `SendMsg`.
- **API:**
  ```go
  func (c *Client) Copy(ctx, protocol string, clientFD, backendFD int) error   // fire-and-forget
  func (c *Client) SSHAccept(ctx, clientFD int) error                           // fire-and-forget
  func (c *Client) TLSAccept(ctx, clientFD int) error                           // fire-and-forget
  func (c *Client) ListenForward(ctx, listen, target string) (id string, err error)
  func (c *Client) Stop(ctx, id string) error
  func (c *Client) Status(ctx) (conns, forwards int, draining bool, err error)
  ```
  Fire-and-forget sends: a successful `sendmsg` means dpipe owns the fd(s) (kernel
  dup'd them). On `sendmsg` failure the caller closes the conn(s) and drops the
  ingress connection.

---

## 7. Ingress handling
### 7.1 TCP (opaque)
```
for conn := tcpLn.Accept(): go {
    backend, err := net.DialTimeout("tcp", route.target, dial_timeout)
    if err { conn.Close(); return }
    handoffCopy(conn, backend, "tcp")            // §8
}
```
### 7.2 HTTP (plaintext, sniff Host)
```
for conn := httpLn.Accept(): go {
    conn.SetReadDeadline(now+http_sniff_timeout)
    buf, host, err := httpsniff.ReadHeaderBlock(conn, http_sniff_max_bytes)
    conn.SetReadDeadline(zero)
    if err { writeQuick(conn,400); conn.Close(); return }
    if t, ok := acmeTarget(buf); ok { pipeTo(conn, t, buf); return }  // §7.2.1
    target, ok := router.HostBackend(host)
    if !ok { writeQuick(conn,502); conn.Close(); return }
    backend, err := net.DialTimeout("tcp", target, dial_timeout)
    if err { writeQuick(conn,502); conn.Close(); return }
    if _, err := backend.Write(buf); err != nil { conn.Close(); backend.Close(); return }
    handoffCopy(conn, backend, "http")
}
```
#### 7.2.1 ACME challenge
When `acme.challenge_target` is set, a request whose path starts with
`/.well-known/acme-challenge/` goes there instead of to the host map, for every
Host and before any routing or auth decision. This is deliberate: a certificate
authority validates a custom domain before a certificate for it exists, so the
request cannot arrive over TLS and the name may have no route yet. The cost is
that a guest cannot serve that path itself over plaintext http.

### 7.3 HTTPS (accept raw, hand off; TLS terminates in dpipe)
```
for conn := httpsLn.Accept(): go {
    err := handoffTLSAccept(conn)   // §8; do NOT read/write conn here
    conn.Close()
    if err { log.Warn("tls_accept handoff failed", err) }
}
```
### 7.4 SSH (accept raw, hand off)
```
for conn := sshLn.Accept(): go {
    err := handoffSSHAccept(conn)   // §8
    conn.Close()
    if err { log.Warn("ssh_accept handoff failed", err) }
}
```

---

## 8. Handoff (`handoff.go`)
```
func handoffCopy(client, backend net.Conn, proto string):
    sc1 := client.(syscall.Conn).SyscallConn(); sc2 := backend.(syscall.Conn).SyscallConn()
    var e error
    sc1.Control(func(c uintptr){ sc2.Control(func(b uintptr){ e = ctrl.Copy(ctx, proto, int(c), int(b)) })})
    client.Close(); backend.Close()
    if e != nil { log.Warn("copy handoff failed", e) }

func handoffSSHAccept(client net.Conn) error:  // one fd
    sc := client.(syscall.Conn).SyscallConn(); var e error
    sc.Control(func(c uintptr){ e = ctrl.SSHAccept(ctx, int(c)) }); return e

func handoffTLSAccept(client net.Conn) error:  // one fd
    sc := client.(syscall.Conn).SyscallConn(); var e error
    sc.Control(func(c uintptr){ e = ctrl.TLSAccept(ctx, int(c)) }); return e
```
Use `SyscallConn().Control` (raw fd; no dup; no blocking-mode change). `sendmsg`
inside the callback while fds are live; kernel dups into dpipe; dproxy `Close()`s
so dpipe is sole owner. For TLS/SSH the handed-off socket is raw TCP (no crypto
state yet), which is why it's fd-passable — dpipe then runs the TLS/SSH server on
it.

---

## 9. Resolve handler (`resolver.go`)
```
func Handle(m Msg) Msg:
    switch m.Kind:
    case "ssh":
        key := normalize(m.SSHPubKey)                  // ssh.ParseAuthorizedKey, re-marshal w/o comment
        if u, ok := users[{key, m.SSHUser}]; ok:       // login name selects which VM
            log.Info("ssh authorize", vm=m.SSHUser, fp=m.SSHFingerprint, client=m.ClientIP, target=u.target)
            return resolved{ID:m.ID, Authorized:true, Target:u.target, RemoteUser:u.remoteUser}
        if owned := ownedBy[key]; len(owned) > 0:      // known key, no VM named
            return resolved{ID:m.ID, Authorized:false, Notice:vmList(m.SSHUser, owned)}
        return resolved{ID:m.ID, Authorized:false}
    case "http":
        req := requestFrom(m)                           // the details dpipe forwarded
        if consoleVMHost(m.Host) is a published vm:      // a console name is dproxy's own
            v := authorizeConsole(...)                  // same policy as plaintext console
            return resolved{ID:m.ID, Authorized:true, Protocol:"console",
                            Target:v.target, RemoteUser:v.remoteUser, Sub:v.sub, WSKey:v.wsKey}
        t, ok := router.HostBackend(m.Host)             // same host map as plaintext http
        if !ok: return resolved{ID:m.ID, Authorized:false}
        v := authorizeRequest(router, auth, m.Host, req)             // same policy as plaintext http
        if !v.ok: return resolved{ID:m.ID, Status:v.status, Location:v.location, SetCookie:v.setCookie}
        return resolved{ID:m.ID, Authorized:true, Target:t}
    default: return error{ID:m.ID, Error:"unknown resolve kind"}
```
The host map is the single source of truth for HTTP routing: the plaintext path
reads it directly; the HTTPS path reads it via `resolve`. The auth policy
(`unauthenticated_ports`, cookie verification, the login round trip) is likewise
one implementation, `authorizeRequest` in `auth.go`: on plaintext dproxy writes
the verdict to the client itself, on https it returns it to dpipe, which never
holds the cookie secret or the per-host port policy. SSH authorizes on the
{pubkey, login name} pair: the key says who you are, the login name says which of
your VMs you want, and neither alone is enough. A known key that named no VM of
its own gets `notice` instead of a target — the list of names it may use, never
anyone else's — which dpipe prints on the session before hanging up.

---

## 10. Scope notes
- **TLS and SSH terminate in dpipe**; dproxy only accepts raw sockets and
  answers `resolve`. This preserves redeploy-safety uniformly across HTTPS, SSH,
  and handed-off TCP/HTTP.
- **HTTP/1.1 only** over TLS (dpipe forces it via ALPN); no HTTP/2, no mTLS, no
  ACME in v1.
- **HTTP (plaintext) routes per-connection** by first `Host`.
- **TCP is opaque**.

---

## 11. Error handling
- Backend dial fail: HTTP → 502; TCP → close. (HTTPS/SSH backend dials happen in
  dpipe.)
- Control conn down mid-handoff: close conn(s), drop ingress; new ingress during
  outage fails fast.
- `resolve` while policy/host-map unloaded: reply `authorized:false`.
- HTTP sniff timeout/oversize/malformed: 400.

---

## 12. Observability & security
- `log/slog`, per-connection `id` (reused as the `copy`/`ssh_accept`/`tls_accept`
  message id). Log accept, route decision, dial result, handoff result, and
  `resolve` decisions (host/SNI or user/fingerprint + target — never secrets or
  payloads).
- Fail closed: empty HTTP `default` → 502; unknown SSH pubkey / unknown HTTPS host →
  unauthorized. An SSH `notice` names only the VMs the offered key owns, so a
  session never learns another user's VM exists.
- Treat `ssh.users` and the host map as sensitive routing policy.
- Validate all `target`/`hosts` are `host:port`; reject wildcards.

---

## 13. Testing (acceptance criteria)
**Unit:** `httpsniff.ReadHeaderBlock`; `router`; `resolver.Handle` (ssh known/unknown
+ key normalization; one key over many VMs; a login name naming no owned VM → `notice`;
a login name naming another user's VM → refused, and not named in the notice;
http host hit/miss; http auth on a protected host — no cookie,
forged cookie, cookie for another host, callback token, valid session).
**Integration (with the paired dpipe):**
1. HTTP host routing incl. POST body passthrough.
2. TCP echo.
3. **HTTPS end-to-end (must pass):** `curl https://vm1.local --resolve` to the
   dproxy with a trusted demo CA → correct backend, correct SNI cert, body intact,
   unknown host → 502, HTTP/1.1 enforced.
3b. **Auth on both ingresses (must pass):** a protected host over https → 302 to
   the login URL (401 for a non-browser request); a forged, expired or
   wrong-audience cookie → same refusal; `/__auth/callback?token=…` → 302 with the
   session cookie; that cookie → served; an unauthenticated port → served with no
   cookie at all.
4. **SSH end-to-end (must pass):** `ssh -p 2222 <vm-name>@host` → shell; `exec` exit
   status; `scp`/`sftp`; `-L` forward; unauthorized key rejected; authorized key +
   owned vm name → correct target/remote_user; authorized key + no/unknown vm name →
   the VM list printed and the session closed.
5. **dproxy redeploy-safe (must pass):** with a live HTTPS transfer, a live SSH
   session, and a TCP/HTTP transfer in flight, `SIGTERM` dproxy and start a new
   instance (SO_REUSEPORT); assert all survive and new connections are served with
   no refused gap.
6. **Absorb dpipe upgrade:** during traffic, `dpipe -upgrade`; assert control
   conn drops+reconnects, new handoffs/logins land on the new dpipe, existing
   sessions complete in the old one.

`justfile` + `scripts/demo.sh` (dpipe + dproxy + dummy HTTP/TCP backends + a local
`sshd` + a demo CA and per-SNI certs) + `scripts/keys.sh`.

---

## 14. Definition of done
- Builds on Linux+macOS with Go 1.26.5; `go test -race ./...` green incl.
  integration 1–6.
- `just demo` shows: HTTP host routing, TCP echo, HTTPS via dpipe TLS
  termination, a real SSH shell, a dproxy redeploy preserving a live HTTPS + SSH
  session, and a dpipe upgrade absorbed by reconnect.
