#!/usr/bin/env bash
# End-to-end demo of the proxy + dpipe pair.
#
# Shows: HTTP host routing, TCP echo, a listen_forward, HTTPS terminated in
# dpipe, an SSH shell terminated in dpipe (when a local sshd can be started),
# a proxy redeploy that preserves live sessions, and a dpipe self-upgrade
# absorbed by the proxy's reconnect.
#
# This script is identical in the proxy and dpipe repositories.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
DEMO="$ROOT/demo"
KEYS="$DEMO/keys"
RUN="$DEMO/run"
LOGS="$DEMO/logs"

HTTP_PORT=18080
HTTPS_PORT=18443
TCP_PORT=19000
SSH_PORT=12222
BACKEND1=18001
BACKEND2=18002
ECHO_PORT=19001
FORWARD_PORT=15432
SSHD_PORT=2200

mkdir -p "$RUN" "$LOGS"
PIDS=()

cleanup() {
  echo
  echo "--- shutting down"
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT

need() { command -v "$1" >/dev/null || { echo "missing required tool: $1" >&2; exit 1; }; }
need go
need curl
need python3

wait_for_port() { # host port
  for _ in $(seq 1 100); do
    (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null && { exec 3>&-; return 0; }
    sleep 0.1
  done
  echo "timed out waiting for $1:$2" >&2
  return 1
}

wait_for_file() {
  for _ in $(seq 1 100); do
    [[ -e "$1" ]] && return 0
    sleep 0.1
  done
  echo "timed out waiting for $1" >&2
  return 1
}

echo "--- generating keys and certificates"
DEMO_SSHD_PORT=$SSHD_PORT "$HERE/keys.sh"

echo "--- building binaries"
(cd "$ROOT/dpipe" && go build -o "$RUN/dpipe" ./cmd/dpipe)
(cd "$ROOT/proxy" && go build -o "$RUN/proxy" ./cmd/proxy)

echo "--- starting dummy backends"
python3 "$HERE/backends.py" http "$BACKEND1" vm1 >"$LOGS/backend1.log" 2>&1 &
PIDS+=($!)
python3 "$HERE/backends.py" http "$BACKEND2" vm2 >"$LOGS/backend2.log" 2>&1 &
PIDS+=($!)
python3 "$HERE/backends.py" echo "$ECHO_PORT" >"$LOGS/echo.log" 2>&1 &
PIDS+=($!)
wait_for_port 127.0.0.1 "$BACKEND1"
wait_for_port 127.0.0.1 "$BACKEND2"
wait_for_port 127.0.0.1 "$ECHO_PORT"

# An unprivileged sshd for the SSH part of the demo, if the host has one.
SSHD_BIN=${SSHD_BIN:-$(command -v sshd || echo /usr/sbin/sshd)}
SSH_READY=0
if [[ -x "$SSHD_BIN" ]]; then
  mkdir -p "$RUN/sshd"
  cat "$KEYS/alice.pub" "$KEYS/bob.pub" >"$RUN/sshd/authorized_keys"
  # dpipe authenticates to the backend with its own client key.
  cat "$KEYS/dpipe_client_ed25519.pub" >>"$RUN/sshd/authorized_keys"
  chmod 600 "$RUN/sshd/authorized_keys"
  cat >"$RUN/sshd/sshd_config" <<EOF
Port $SSHD_PORT
ListenAddress 127.0.0.1
HostKey $KEYS/dpipe_host_ed25519
PidFile $RUN/sshd/sshd.pid
AuthorizedKeysFile $RUN/sshd/authorized_keys
PasswordAuthentication no
UsePAM no
StrictModes no
Subsystem sftp internal-sftp
EOF
  "$SSHD_BIN" -f "$RUN/sshd/sshd_config" -D >"$LOGS/sshd.log" 2>&1 &
  PIDS+=($!)
  if wait_for_port 127.0.0.1 "$SSHD_PORT" 2>/dev/null; then
    SSH_READY=1
    ssh-keyscan -T 2 -p "$SSHD_PORT" 127.0.0.1 >"$KEYS/known_hosts" 2>/dev/null || true
  fi
fi
if [[ "$SSH_READY" != 1 ]]; then
  echo "!!! no usable sshd on this host: the SSH part of the demo is skipped"
fi

echo "--- writing configuration"
cat >"$RUN/dpipe.yaml" <<EOF
control_socket: $RUN/control.sock
upgrade_socket: $RUN/upgrade.sock
drain_timeout: 0s
log_level: info

ssh:
  enabled: $([[ "$SSH_READY" == 1 ]] && echo true || echo false)
  host_key: $KEYS/dpipe_host_ed25519
  client_key: $KEYS/dpipe_client_ed25519
  backend_known_hosts: ""
  dial_timeout: 5s
  resolve_timeout: 3s

tls:
  enabled: true
  certs:
    - sni: vm1.local
      cert: $KEYS/vm1.local.crt
      key: $KEYS/vm1.local.key
    - sni: vm2.local
      cert: $KEYS/vm2.local.crt
      key: $KEYS/vm2.local.key
  default_cert: $KEYS/default.local.crt
  default_key: $KEYS/default.local.key
  min_version: "1.2"
  dial_timeout: 5s
  resolve_timeout: 3s
  sniff_timeout: 5s
  sniff_max_bytes: 65536
EOF

cat >"$RUN/proxy.yaml" <<EOF
control_socket: $RUN/control.sock

http:
  listen: "127.0.0.1:$HTTP_PORT"
  reuseport: true
  hosts:
    vm1.local: "127.0.0.1:$BACKEND1"
    vm2.local: "127.0.0.1:$BACKEND2"
  default: ""

https:
  listen: "127.0.0.1:$HTTPS_PORT"
  reuseport: true

tcp:
  - listen: "127.0.0.1:$TCP_PORT"
    target: "127.0.0.1:$ECHO_PORT"
    protocol: tcp
    reuseport: true

$(if [[ "$SSH_READY" == 1 ]]; then cat <<SSHCFG
ssh:
  listen: "127.0.0.1:$SSH_PORT"
  reuseport: true
  users:
    - pubkey_file: $KEYS/alice.pub
      target: "127.0.0.1:$SSHD_PORT"
      remote_user: "$USER"
SSHCFG
fi)

listen_forwards:
  - listen: "127.0.0.1:$FORWARD_PORT"
    target: "127.0.0.1:$BACKEND1"

dial_timeout: 5s
http_sniff_timeout: 5s
http_sniff_max_bytes: 65536
log_level: info
EOF

echo "--- starting dpipe"
"$RUN/dpipe" -config "$RUN/dpipe.yaml" >"$LOGS/dpipe.log" 2>&1 &
DPIPE_PID=$!
PIDS+=("$DPIPE_PID")
wait_for_file "$RUN/control.sock"

echo "--- starting proxy"
"$RUN/proxy" -config "$RUN/proxy.yaml" >"$LOGS/proxy.log" 2>&1 &
PROXY_PID=$!
PIDS+=("$PROXY_PID")
wait_for_port 127.0.0.1 "$HTTP_PORT"
wait_for_port 127.0.0.1 "$HTTPS_PORT"

echo
echo "=== 1. HTTP host routing"
curl -sS -H "Host: vm1.local" "http://127.0.0.1:$HTTP_PORT/hello"
curl -sS -H "Host: vm2.local" "http://127.0.0.1:$HTTP_PORT/hello"
curl -sS -X POST -d "body-bytes" -H "Host: vm1.local" "http://127.0.0.1:$HTTP_PORT/submit"
echo "unknown host ->"
curl -sS -o /dev/null -w "  status %{http_code}\n" -H "Host: nope.local" "http://127.0.0.1:$HTTP_PORT/"

echo
echo "=== 2. TCP echo"
printf 'opaque-bytes\n' | timeout 5 python3 -c '
import socket, sys
s = socket.create_connection(("127.0.0.1", '"$TCP_PORT"'))
s.sendall(sys.stdin.buffer.read())
s.shutdown(socket.SHUT_WR)
sys.stdout.write("  echoed: " + s.recv(4096).decode())
'

echo
echo "=== 3. listen_forward (dpipe-owned listener)"
curl -sS -H "Host: vm1.local" "http://127.0.0.1:$FORWARD_PORT/forwarded" | sed 's/^/  /'

echo
echo "=== 4. HTTPS terminated in dpipe"
curl -sS --cacert "$KEYS/ca.crt" --resolve "vm1.local:$HTTPS_PORT:127.0.0.1" \
  "https://vm1.local:$HTTPS_PORT/secure" | sed 's/^/  /'
curl -sS --cacert "$KEYS/ca.crt" --resolve "vm2.local:$HTTPS_PORT:127.0.0.1" \
  "https://vm2.local:$HTTPS_PORT/secure" | sed 's/^/  /'
echo "  http version negotiated:"
curl -sS -o /dev/null --cacert "$KEYS/ca.crt" --resolve "vm1.local:$HTTPS_PORT:127.0.0.1" \
  -w "    %{http_version}\n" "https://vm1.local:$HTTPS_PORT/secure"
echo "  unknown host over TLS:"
curl -sS -k -o /dev/null --resolve "nope.local:$HTTPS_PORT:127.0.0.1" \
  -w "    status %{http_code}\n" "https://nope.local:$HTTPS_PORT/" || true

if [[ "$SSH_READY" == 1 ]]; then
  echo
  echo "=== 5. SSH terminated in dpipe"
  SSH_OPTS=(-p "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
            -o IdentitiesOnly=yes -i "$KEYS/alice")
  ssh "${SSH_OPTS[@]}" "$USER@127.0.0.1" 'echo "  remote shell says: $(hostname) as $(whoami)"' || true
  echo "  exec exit status:"
  ssh "${SSH_OPTS[@]}" "$USER@127.0.0.1" 'exit 42' || echo "    got $?"
  echo "  scp:"
  echo "scp-payload" >"$RUN/scp-src.txt"
  scp -q -P "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o IdentitiesOnly=yes -i "$KEYS/alice" "$RUN/scp-src.txt" "$USER@127.0.0.1:$RUN/scp-dst.txt" \
    && echo "    transferred: $(cat "$RUN/scp-dst.txt")"
  echo "  unauthorized key (bob):"
  ssh -p "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o IdentitiesOnly=yes -o BatchMode=yes -i "$KEYS/bob" "$USER@127.0.0.1" true \
    2>/dev/null && echo "    UNEXPECTEDLY ACCEPTED" || echo "    rejected, as expected"
fi

echo
echo "=== 6. proxy redeploy (SO_REUSEPORT) with traffic in flight"
# A slow HTTPS transfer plus a long-lived TCP connection, both handed off already.
(curl -sS --cacert "$KEYS/ca.crt" --resolve "vm1.local:$HTTPS_PORT:127.0.0.1" \
  "https://vm1.local:$HTTPS_PORT/slow" >"$LOGS/inflight-https.log" 2>&1) &
INFLIGHT=$!
timeout 20 python3 -c '
import socket, time
s = socket.create_connection(("127.0.0.1", '"$TCP_PORT"'))
time.sleep(6)
s.sendall(b"survived-redeploy")
print("  tcp session after redeploy:", s.recv(4096).decode())
' &
TCP_INFLIGHT=$!
sleep 1

"$RUN/proxy" -config "$RUN/proxy.yaml" >"$LOGS/proxy2.log" 2>&1 &
PROXY2_PID=$!
PIDS+=("$PROXY2_PID")
sleep 1
kill "$PROXY_PID" 2>/dev/null || true
echo "  old proxy terminated, new proxy serving:"
curl -sS -H "Host: vm1.local" "http://127.0.0.1:$HTTP_PORT/after-redeploy" | sed 's/^/    /'
wait "$INFLIGHT" 2>/dev/null || true
wait "$TCP_INFLIGHT" 2>/dev/null || true

echo
echo "=== 7. dpipe self-upgrade absorbed by the proxy"
"$RUN/dpipe" -config "$RUN/dpipe.yaml" -upgrade >"$LOGS/dpipe2.log" 2>&1 &
DPIPE2_PID=$!
PIDS+=("$DPIPE2_PID")
sleep 2
echo "  new dpipe serving new work:"
curl -sS --cacert "$KEYS/ca.crt" --resolve "vm1.local:$HTTPS_PORT:127.0.0.1" \
  "https://vm1.local:$HTTPS_PORT/after-upgrade" | sed 's/^/    /'
curl -sS -H "Host: vm1.local" "http://127.0.0.1:$FORWARD_PORT/forward-after-upgrade" | sed 's/^/    /'
if kill -0 "$DPIPE_PID" 2>/dev/null; then
  echo "  old dpipe still draining"
else
  echo "  old dpipe drained and exited"
fi

echo
echo "--- logs are in $LOGS"
