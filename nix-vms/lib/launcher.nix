# Builds a per-VM launcher script with start/stop/status subcommands.
# Used by both the flake's `nix run` apps and the NixOS systemd services.
{ pkgs, name, vm }:

let
  lib = pkgs.lib;

  # tap queue-mode must match vCPU count: multi-queue for >1 vCPU.
  mq = vm.cpu > 1;

  shares = vm.shares or [ ];
  guestUser = vm.guestUser or null;
  bootCommand = vm.bootCommand or null;

  # extra environment for the boot service, as an attrset of KEY = "value".
  # systemd runs the unit with a bare PATH (/usr/{local/,}{s,}bin), so a boot
  # command living outside those dirs (e.g. `air` in /go/bin, put on PATH only
  # via the interactive shell's .bashrc) needs PATH set explicitly here.
  bootEnv = vm.bootEnv or { };
  bootEnvLines = lib.concatStringsSep "\n" (
    lib.mapAttrsToList (k: v: "Environment=${k}=${v}") bootEnv
  );

  # ephemeral: root writes go to a throwaway overlay, discarded on shutdown;
  # the base image stays pristine (every boot is identical).
  ephemeral = vm.ephemeral or false;

  hasShares = shares != [ ];

  # virtiofs device args, one chardev+device pair per share. Each chardev
  # connects to a virtiofsd daemon the launcher spawns (see `start`), exposing
  # its socket at $STATE_DIR/virtiofs-<i>.sock.
  shareArgs = lib.concatStringsSep "\n" (lib.imap0 (
    i: s: ''          -chardev "socket,id=fs${toString i},path=$STATE_DIR/virtiofs-${toString i}.sock"
          -device "vhost-user-fs-pci,queue-size=1024,chardev=fs${toString i},tag=${s.tag}"''
  ) shares);

  # vhost-user (virtiofs) requires the guest's RAM to live in shareable memory,
  # so virtiofsd can map it. Back main memory with a memfd (share=on) and point
  # the machine at it — only when there are shares, to keep share-less VMs lean.
  memBackendArgs = lib.optionalString hasShares ''
          -object "memory-backend-memfd,id=mem,size=${toString vm.mem}M,share=on"'';
  machineOpts = "microvm,acpi=on,rtc=on,pcie=on" + lib.optionalString hasShares ",memory-backend=mem";

  # virtiofsd startup, one daemon per share; runs before qemu and outlives this
  # script (backgrounded, orphaned to init on `nix run`; held in the cgroup
  # under systemd). --sandbox=none mirrors 9p security_model=none: no uid
  # mapping, so the guest sees the host's numeric uid/gid.
  virtiofsdStart = lib.concatStringsSep "\n" (lib.imap0 (i: s: ''
        rm -f "$STATE_DIR/virtiofs-${toString i}.sock" "$STATE_DIR/virtiofs-${toString i}.pid"
        virtiofsd \
          --socket-path="$STATE_DIR/virtiofs-${toString i}.sock" \
          --shared-dir="${s.source}" \
          --sandbox=none &
        echo "$!" > "$STATE_DIR/virtiofs-${toString i}.pid"
        # qemu needs the socket present before it can connect
        for _ in $(seq 1 50); do [ -S "$STATE_DIR/virtiofs-${toString i}.sock" ] && break; sleep 0.1; done
  '') shares);

  # systemd unit that runs the boot command as the guest user in the app dir.
  bootUnit =
    if bootCommand == null then
      null
    else
      pkgs.writeText "microqemu-boot.service" ''
        [Unit]
        Description=microqemu boot command for ${name}
        RequiresMountsFor=${vm.bootWorkingDir}
        After=network-online.target
        Wants=network-online.target

        [Service]
        Type=simple
        User=${guestUser.name}
        WorkingDirectory=${vm.bootWorkingDir}
        ${bootEnvLines}
        ExecStart=/bin/sh -c "${vm.bootCommand}"
        Restart=on-failure
        RestartSec=2

        [Install]
        WantedBy=multi-user.target
      '';

  # Offline rootfs image builder. Runs under fakeroot so system files stay
  # root-owned and the guest user's home is owned correctly, without real root.
  # $1 = extraction dir, $2 = output image.
  mkrootfs = pkgs.writeShellScript "microqemu-${name}-mkrootfs" ''
    set -euo pipefail
    tmp="$1"; img="$2"

    tar -xf "${vm.rootfsTar}" -C "$tmp"

    # DNS
    rm -f "$tmp/etc/resolv.conf"
    echo 'nameserver ${vm.dns}' > "$tmp/etc/resolv.conf"

    # hostname + hosts entry (kernel ip= sets the hostname to '${name}', but
    # nothing maps it to a local address, so sudo/etc. warn "unable to resolve
    # host ${name}"). Point it at loopback.
    echo '${name}' > "$tmp/etc/hostname"
    {
      echo '127.0.0.1 localhost'
      echo '127.0.1.1 ${name}'
    } > "$tmp/etc/hosts"

    # shares: mountpoint + fstab entry (nofail so a bad mount never blocks boot)
    ${lib.concatMapStrings (s: ''
      mkdir -p "$tmp${s.mountPoint}"
      echo '${s.tag} ${s.mountPoint} virtiofs nofail,_netdev 0 0' >> "$tmp/etc/fstab"
    '') shares}
    ${lib.optionalString (guestUser != null) ''
      # guest user — uid/gid must match the host owner of the shared dir, since
      # virtiofsd --sandbox=none shows the host's numeric uid/gid in the guest.
      if ! grep -q '^${guestUser.name}:' "$tmp/etc/passwd"; then
        echo '${guestUser.name}:x:${toString guestUser.uid}:${toString guestUser.gid}::${guestUser.home}:/bin/bash' >> "$tmp/etc/passwd"
        echo '${guestUser.name}:x:${toString guestUser.gid}:' >> "$tmp/etc/group"
        echo '${guestUser.name}:!:20000:0:99999:7:::' >> "$tmp/etc/shadow"
      fi
      mkdir -p "$tmp${guestUser.home}"
      chown -R ${toString guestUser.uid}:${toString guestUser.gid} "$tmp${guestUser.home}"
    ''}
    ${lib.optionalString (bootCommand != null) ''
      # boot service (runs the command at every boot)
      install -Dm644 ${bootUnit} "$tmp/etc/systemd/system/microqemu-boot.service"
      mkdir -p "$tmp/etc/systemd/system/multi-user.target.wants"
      ln -sf ../microqemu-boot.service "$tmp/etc/systemd/system/multi-user.target.wants/microqemu-boot.service"
    ''}

    mkfs.ext4 -F -q -d "$tmp" "$img"
  '';
in
pkgs.writeShellApplication {
  name = "microqemu-${name}";
  runtimeInputs = with pkgs; [
    qemu_kvm
    virtiofsd
    socat
    coreutils
    e2fsprogs
    gnutar
    gnugrep
    gawk
    fakeroot
    util-linux
  ];
  text = ''
    set -euo pipefail

    # systemd sets STATE_DIRECTORY; manual `nix run` falls back to CWD.
    STATE_DIR="''${MICROQEMU_STATE_DIR:-''${STATE_DIRECTORY:-$PWD/.microqemu/${name}}}"
    mkdir -p "$STATE_DIR"
    IMG="$STATE_DIR/rootfs.ext4"
    QMP="$STATE_DIR/qmp.sock"
    PID="$STATE_DIR/qemu.pid"

    is_running() { [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; }

    build_image() {
      if [ ! -f "$IMG" ]; then
        echo "microqemu(${name}): building rootfs image from ${vm.rootfsTar} -> $IMG" >&2
        tmp="$(mktemp -d)"
        truncate -s ${vm.diskSize} "$IMG"
        fakeroot ${mkrootfs} "$tmp" "$IMG"
        rm -rf "$tmp"
      fi
    }

    qmp_cmd() {
      printf '%s\n%s\n' '{"execute":"qmp_capabilities"}' "$1" \
        | socat - "UNIX-CONNECT:$QMP" >/dev/null 2>&1
    }
${lib.optionalString hasShares ''
    kill_virtiofs() {
      for f in "$STATE_DIR"/virtiofs-*.pid; do
        [ -e "$f" ] || continue
        kill "$(cat "$f")" 2>/dev/null || true
        rm -f "$f"
      done
    }
''}

    case "''${1:-start}" in
      start)
        if is_running; then echo "microqemu(${name}): already running"; exit 0; fi
        build_image
        rm -f "$QMP" "$PID"
        ${lib.optionalString ephemeral ''
        # keep the disposable snapshot overlay on disk (not RAM-backed /tmp)
        export TMPDIR="$STATE_DIR"''}
${virtiofsdStart}
        args=(
          -pidfile "$PID" -no-reboot
${memBackendArgs}
          -machine "${machineOpts}"
          -enable-kvm -cpu host
          -m ${toString vm.mem} -smp ${toString vm.cpu}
          -kernel "${vm.kernel}"
          -append "console=ttyS0 root=/dev/vda rw ip=${vm.ip}::${vm.gateway}:${vm.netmask}:${name}:eth0:off reboot=t quiet loglevel=3 tsc=reliable no_timer_check rcupdate.rcu_expedited=1"
          -drive "id=root,file=$IMG,format=raw,if=none${lib.optionalString ephemeral ",snapshot=on"}"
          -device "virtio-blk-pci,drive=root"
          -netdev "tap,id=net0,ifname=${vm.tap},script=no,downscript=no,queues=${toString vm.cpu}"
          -device "virtio-net-pci,netdev=net0,mac=${vm.mac}${if mq then ",mq=on" else ""}"
          -device "virtio-rng-pci"
${shareArgs}
          -qmp "unix:$QMP,server=on,wait=off"
          -nodefaults -no-user-config -display none
          # serial on a unix socket (never the terminal; access is via ssh),
          # mirrored to a log file so boot output is captured even when no one
          # is attached. Attach interactively later with the `console` command.
          -chardev "socket,id=serial0,path=$STATE_DIR/console.sock,server=on,wait=off,logfile=$STATE_DIR/console.log"
          -serial chardev:serial0
        )
        if [ -n "''${INVOCATION_ID:-}" ]; then
          # running under systemd: stay in the foreground so it can supervise
          exec qemu-system-x86_64 "''${args[@]}"
        else
          # manual `nix run`: daemonize and hand the prompt back
          qemu-system-x86_64 -daemonize "''${args[@]}"
          echo "microqemu(${name}): started (pid $(cat "$PID"))"
          echo "  ssh:     ssh root@${vm.ip}"
          echo "  console: microqemu-${name} console   (log: $STATE_DIR/console.log)"
          echo "  stop:    microqemu-${name} stop"
        fi
        ;;
      console)
        if ! is_running; then echo "microqemu(${name}): not running"; exit 1; fi
        echo "microqemu(${name}): attaching to console — press Ctrl-] to detach" >&2
        exec socat -,raw,echo=0,escape=0x1d "UNIX-CONNECT:$STATE_DIR/console.sock"
        ;;
      stop)
        if ! is_running; then
          ${lib.optionalString hasShares "kill_virtiofs"}
          echo "microqemu(${name}): not running"; exit 0
        fi
        stopped=no
        # 1) graceful ACPI powerdown (works only if the guest kernel supports it)
        qmp_cmd '{"execute":"system_powerdown"}' || true
        for _ in $(seq 1 15); do is_running || { stopped=yes; break; }; sleep 1; done
        # 2) fall back to a hard QMP quit
        if [ "$stopped" = no ]; then
          echo "microqemu(${name}): powerdown timed out, forcing quit" >&2
          qmp_cmd '{"execute":"quit"}' || true
          for _ in $(seq 1 5); do is_running || { stopped=yes; break; }; sleep 1; done
        fi
        # 3) last resort
        [ "$stopped" = yes ] || kill -9 "$(cat "$PID")" 2>/dev/null || true
        ${lib.optionalString hasShares "kill_virtiofs"}
        echo "microqemu(${name}): stopped"
        ;;
      status)
        if is_running; then echo "running"; else echo "stopped"; exit 3; fi
        ;;
      stats)
        if ! is_running; then echo "microqemu(${name}): not running"; exit 1; fi
        p="$(cat "$PID")"
        clk="$(getconf CLK_TCK 2>/dev/null || echo 100)"

        # CPU: sample utime+stime (ticks) over a 1s window -> % of one core
        c0="$(awk '{print $14 + $15}' "/proc/$p/stat")"
        sleep 1
        c1="$(awk '{print $14 + $15}' "/proc/$p/stat")"
        cpu="$(awk -v a="$c0" -v b="$c1" -v hz="$clk" 'BEGIN { printf "%.2f", (b - a) / hz * 100 }')"

        # MEM: qemu RSS vs the configured guest RAM
        rss_kb="$(awk '/^VmRSS:/ { print $2 }' "/proc/$p/status")"
        usage=$(( rss_kb * 1024 ))
        limit=$(( ${toString vm.mem} * 1024 * 1024 ))
        mempct="$(awk -v u="$usage" -v l="$limit" 'BEGIN { printf "%.2f", u / l * 100 }')"

        # NET: tap counters, shown from the guest's perspective
        # (guest RX = bytes host sent to tap = tap tx; guest TX = tap rx)
        net_rx="$(cat "/sys/class/net/${vm.tap}/statistics/tx_bytes" 2>/dev/null || echo 0)"
        net_tx="$(cat "/sys/class/net/${vm.tap}/statistics/rx_bytes" 2>/dev/null || echo 0)"

        # BLOCK: bytes the qemu process read/wrote at the block layer
        blk_r="$(awk '/^read_bytes:/ { print $2 }' "/proc/$p/io" 2>/dev/null || echo 0)"
        blk_w="$(awk '/^write_bytes:/ { print $2 }' "/proc/$p/io" 2>/dev/null || echo 0)"

        # PIDS: qemu threads (vCPUs + helpers)
        tasks=(/proc/"$p"/task/*)
        npids="''${#tasks[@]}"

        human() { awk -v b="$1" 'BEGIN { split("B KiB MiB GiB TiB", u, " "); i = 1; while (b >= 1024 && i < 5) { b /= 1024; i++ } printf "%.1f%s", b, u[i] }'; }

        printf '%-16s %-8s %-21s %-7s %-19s %-19s %s\n' \
          NAME "CPU %" "MEM USAGE / LIMIT" "MEM %" "NET I/O" "BLOCK I/O" PIDS
        printf '%-16s %-8s %-21s %-7s %-19s %-19s %s\n' \
          "${name}" "$cpu%" "$(human "$usage") / $(human "$limit")" "$mempct%" \
          "$(human "$net_rx") / $(human "$net_tx")" "$(human "$blk_r") / $(human "$blk_w")" "$npids"
        ;;
      *)
        echo "usage: microqemu-${name} {start|stop|status|console|stats}" >&2; exit 2
        ;;
    esac
  '';
}
