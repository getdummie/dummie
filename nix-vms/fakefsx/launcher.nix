{ pkgs, vm }:

pkgs.writeShellApplication {
  name = "microqemu-fakefsx";
  runtimeInputs = with pkgs; [
    socat
    coreutils
    util-linux
  ];
  text = ''
    STATE_DIR="''${MICROQEMU_STATE_DIR:-$PWD/.microqemu/fakefsx}"
    mkdir -p "$STATE_DIR"
    QMP="$STATE_DIR/qmp.sock"
    PID="$STATE_DIR/qemu.pid"
    CONSOLE="$STATE_DIR/console.sock"

    is_running() { [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; }

    qmp_cmd() {
      printf '%s\n%s\n' '{"execute":"qmp_capabilities"}' "$1" \
        | socat - "UNIX-CONNECT:$QMP" >/dev/null 2>&1
    }

    case "''${1:-start}" in
      start)
        if is_running; then echo "microqemu(fakefsx): already running"; exit 0; fi
        rm -f "$QMP" "$PID" "$CONSOLE"
        NIX_DISK_IMAGE="$STATE_DIR/fakefsx.qcow2" TMPDIR="$STATE_DIR" setsid -f \
          ${vm}/bin/run-fakefsx-vm \
          -pidfile "$PID" \
          -monitor none \
          -qmp "unix:$QMP,server=on,wait=off" \
          -chardev "socket,id=serial0,path=$CONSOLE,server=on,wait=off,logfile=$STATE_DIR/console.log" \
          -serial chardev:serial0 \
          </dev/null >"$STATE_DIR/qemu.log" 2>&1
        for _ in $(seq 1 100); do is_running && break; sleep 0.1; done
        if ! is_running; then
          echo "microqemu(fakefsx): failed to start, see $STATE_DIR/qemu.log" >&2
          exit 1
        fi
        echo "microqemu(fakefsx): started (pid $(cat "$PID"))"
        echo "  console: microqemu-fakefsx console   (log: $STATE_DIR/console.log)"
        echo "  stop:    microqemu-fakefsx stop"
        ;;
      console)
        if ! is_running; then echo "microqemu(fakefsx): not running"; exit 1; fi
        echo "microqemu(fakefsx): attaching to console — press Ctrl-] to detach" >&2
        exec socat -,raw,echo=0,escape=0x1d "UNIX-CONNECT:$CONSOLE"
        ;;
      stop)
        if ! is_running; then echo "microqemu(fakefsx): not running"; exit 0; fi
        qmp_cmd '{"execute":"system_powerdown"}' || true
        for _ in $(seq 1 30); do is_running || break; sleep 1; done
        if is_running; then
          echo "microqemu(fakefsx): powerdown timed out, forcing quit" >&2
          qmp_cmd '{"execute":"quit"}' || true
          sleep 2
        fi
        if is_running; then kill -9 "$(cat "$PID")" 2>/dev/null || true; fi
        echo "microqemu(fakefsx): stopped"
        ;;
      status)
        if is_running; then echo "running"; else echo "stopped"; exit 3; fi
        ;;
      *)
        echo "usage: microqemu-fakefsx {start|stop|status|console}" >&2; exit 2
        ;;
    esac
  '';
}
