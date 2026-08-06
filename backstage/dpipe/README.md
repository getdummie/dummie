# dpipe

The data plane described in [SPEC.md](SPEC.md). A long-lived process that owns
network connections and moves their bytes, taking commands from `proxy` over a
unix socket. It stays byte-opaque for TCP/HTTP and terminates SSH and TLS, the
two protocols that cannot be fd-passed once encrypted.

## Layout

```
cmd/dpipe/main.go
internal/control/       # SHARED with proxy: protocol.go, conn.go, peer.go
internal/httpsniff/     # SHARED with proxy
internal/xnet/          # SHARED with proxy
internal/dpipe/         # server, copy, listenforward, sshterm, sshrelay, tlsterm,
                        # registry, upgrade, config
```

`internal/control`, `internal/httpsniff` and `internal/xnet` are shared *source*:
byte-identical copies live in the `proxy` repository (the two are separate Go
modules). Change them in one place and copy the files across:

```sh
for pkg in control httpsniff xnet; do
  cp internal/$pkg/*.go ../proxy/internal/$pkg/
  sed -i 's|"dpipe/internal/|"proxy/internal/|g' ../proxy/internal/$pkg/*.go
done
```

## Build and test

Dependencies are `golang.org/x/sys`, `golang.org/x/crypto` and `gopkg.in/yaml.v3`
(all listed in `go.mod`). `go.sum` is not committed yet, so run once:

```sh
go mod tidy
just build     # go build -o bin/dpipe ./cmd/dpipe
just test      # go test ./...
just race      # go test -race ./...
just demo      # full proxy + dpipe demo, builds both repos
```

## Running

```sh
bin/dpipe -config dpipe.yaml     # fresh start
bin/dpipe -config dpipe.yaml -upgrade   # take over the running instance
```

`-upgrade` adopts the control, upgrade and `listen_forward` listeners of the
running process; the old process keeps its active connections (copies, SSH
relays, TLS sessions) until they finish, drops proxy control connections so
proxies reconnect to the new instance, and then exits 0. The socket files stay in
place.

## Implementation notes and deviations from the spec

- **Framing.** Messages are bare JSON objects sent one per `sendmsg(2)`, as
  specified. Because AF_UNIX *stream* sockets may coalesce two fd-less messages
  into a single `recvmsg(2)`, `control.Conn` keeps a read buffer and decodes one
  JSON object at a time. The wire format is unchanged; `control.SendMsg` /
  `RecvMsg` remain available for the strictly alternating upgrade handshake.
- **`MAXFDS`.** The control socket carries at most 2 descriptors, as specified.
  The handover handshake needs one descriptor per listening socket (control,
  upgrade, and every `listen_forward`), so the receive path allows up to
  `control.MaxRecvFDs` (64) and each handler validates its own fd count.
- **`stop`** uses the message `id` as the `listen_forward` id (the wire table
  gives it no separate field), so a `listen_forward` is addressed by the id of
  the request that created it.
- **`internal/control/peer.go`** is an addition to the shared package: both sides
  need the same duplex multiplexing (route `ok`/`error`/`resolved` by id,
  dispatch inbound requests), so it lives with the protocol instead of being
  written twice.
- **Control-plane faults never kill the data plane.** `control.Peer` recovers from
  panics in its read loop and in request handlers and reports them as a failed
  control connection, which the far side recovers from by reconnecting. A failed
  `recvmsg(2)` also reports its raw `-1` return through `n`/`oobn` (internal/poll
  passes both through on error), so `Conn.RecvMsg` clamps them before using
  either as a slice bound.
- **SSH channel relay** waits up to 30s after both data directions hit EOF for
  trailing channel requests (`exit-status`) before closing the channel pair.

## Testing status

`go test ./...` covers: control encode/decode, `SendMsg`/`RecvMsg` fd round-trip
over a `socketpair`, message coalescing, `Pipe` half-close, `httpsniff`, `copy`
with an echo backend, `listen_forward` + `stop`, TLS termination end to end
(generated certificate, real `crypto/tls` client, authorized and unknown host),
and the self-upgrade handover (two in-process instances: the old one drains its
live copy and exits, the new one serves new work, socket files survive).

SSH termination end to end needs a real `ssh` client and `sshd`, so it is
exercised by `scripts/demo.sh` (steps 5) rather than by `go test`, along with the
proxy redeploy and the upgrade-under-traffic scenarios.
