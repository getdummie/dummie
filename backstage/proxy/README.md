# proxy

The control plane described in [SPEC.md](SPEC.md). It owns the public ingress
listeners, decides where each connection goes, and hands connections to `dpipe`
so it holds no long-lived connection state and can be redeployed at any time.

- **HTTP** — sniff the first request's `Host`, dial the backend, replay the
  sniffed prefix, hand off `copy`.
- **TCP** — routed by ingress listener, dial, hand off `copy`.
- **HTTPS** — accept the raw socket and hand it to dpipe (`tls_accept`) before
  any TLS byte; answer dpipe's `resolve{kind:"http"}` from the host map.
- **SSH** — accept the raw socket and hand it off (`ssh_accept`); answer
  `resolve{kind:"ssh"}` from the pubkey policy.

## Layout

```
cmd/proxy/main.go
internal/control/       # SHARED with dpipe: protocol.go, conn.go, peer.go
internal/httpsniff/     # SHARED with dpipe
internal/xnet/          # SHARED with dpipe
internal/proxy/         # proxy, router, handoff, resolver, client, config
```

The three shared packages are byte-identical copies of the ones in the `dpipe`
repository (separate Go modules). To sync after a change there:

```sh
for pkg in control httpsniff xnet; do
  cp ../dpipe/internal/$pkg/*.go internal/$pkg/
  sed -i 's|"dpipe/internal/|"proxy/internal/|g' internal/$pkg/*.go
done
```

## Build and test

Dependencies are `golang.org/x/sys`, `golang.org/x/crypto` (only to parse public
keys for the policy map — the proxy does no SSH or TLS I/O) and
`gopkg.in/yaml.v3`. `go.sum` is not committed yet, so run once:

```sh
go mod tidy
just build     # go build -o bin/proxy ./cmd/proxy
just test
just race
just demo      # full proxy + dpipe demo, builds both repos
```

## Running

```sh
bin/proxy -config proxy.yaml
```

Startup order: connect the control socket (retrying with capped backoff), submit
the configured `listen_forwards`, bind every ingress listener (with
`SO_REUSEPORT` when configured), serve. `SIGTERM`/`SIGINT` stops accepting,
closes the control connection and exits — handed-off connections live in dpipe
and are untouched, and a replacement process can already be bound to the same
ports.

If `https` is configured, `dpipe.tls.enabled` must be true and the
certificates must live in dpipe's config; the proxy logs this cross-process
dependency at startup (it cannot verify it).

## Implementation notes and deviations from the spec

- **Client method signatures** take the per-connection `id` as their first
  argument (`Copy(ctx, id, protocol, clientFD, backendFD)` and friends) so the
  message id is the same id used in the logs, as §12 requires.
- **`stop`** addresses a `listen_forward` by the message `id` of the
  `listen_forward` request that created it (the wire table gives `stop` no
  separate field).
- **`internal/control/peer.go`** is an addition to the shared package: the duplex
  multiplexing is identical on both sides, so it lives with the protocol.
- Framing and `MAXFDS` notes are the same as in the dpipe README, including the
  `recvmsg(2)` `-1` clamp and the panic recovery in `control.Peer`.

## Testing status

`go test ./...` covers: `httpsniff`, the router (case/port normalization,
default, miss), the resolver (known/unknown SSH key with comment normalization,
duplicate keys rejected, HTTP host hit/miss, unknown kind), HTTP host routing
with GET and POST body passthrough end to end through a stand-in dpipe that
performs the `copy`, unknown host → 502, TCP echo, `resolve` answered over the
duplex control connection, and that the HTTPS/SSH ingress hands over the raw
pre-crypto socket with the client's first bytes intact.

The scenarios that need real clients and a real dpipe — `curl https://…` with
a demo CA, a real `ssh` shell/`scp`/`-L`, the proxy redeploy with live sessions,
and absorbing a `dpipe -upgrade` — are driven by `scripts/demo.sh`.
