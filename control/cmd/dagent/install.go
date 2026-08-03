package main

import (
  "context"
  "errors"
  "fmt"
  "io"
  "os"
  "os/exec"
  "os/user"
  "path/filepath"

  "github.com/urfave/cli/v3"
)

const (
  installedBinary = "/usr/local/bin/dagent"
  unitPath        = "/etc/systemd/system/dagent.service"
  unitName        = "dagent.service"
)

// unitTemplate is deliberately plain. The daemon does its own signal handling,
// creates its own runtime directory and drops nothing, so there is little for
// systemd to arrange beyond ordering and restarts.
const unitTemplate = `[Unit]
Description=dagent (microvm host agent)
Documentation=https://github.com/
After=network-online.target
Wants=network-online.target
# Docker rewrites the forward chain on start; dagent repairs its own accepts
# afterwards, but starting in this order avoids a window with no vm egress.
After=docker.service

[Service]
Type=exec
ExecStart=%s serve --config %s
Restart=on-failure
RestartSec=2
# The policy lives in the kernel, so a restart does not open anything up.
KillMode=mixed
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
`

func installCommand() *cli.Command {
  return &cli.Command{
    Name:  "install",
    Usage: "install dagent as a system service so the cli does not need sudo",
    Description: "Copies this binary to " + installedBinary + ", creates the " + defaultGroup +
      " group, writes " + unitPath + ", and starts the daemon.\n\n" +
      "Members of the " + defaultGroup + " group can then run vm commands without sudo. That " +
      "grants the ability to start a vm from an arbitrary kernel and an arbitrary tar, which is " +
      "root by another route -- treat it exactly like passwordless sudo.",
    Flags: []cli.Flag{
      &cli.BoolFlag{Name: "skip-doctor", Usage: "install even if preflight checks fail"},
      &cli.StringFlag{Name: "config", Usage: "config path to record in the unit (default " + configPath + ")"},
    },
    Action: func(ctx context.Context, cmd *cli.Command) error {
      return runInstall(ctx, cmd.Bool("skip-doctor"), orDefault(cmd.String("config"), configPath))
    },
  }
}

func runInstall(ctx context.Context, skipDoctor bool, cfgPath string) error {
  if os.Geteuid() != 0 {
    return errors.New("install needs root")
  }

  // A host that cannot run a VM should not get a service that pretends it can.
  if !skipDoctor {
    fmt.Println("running preflight checks")
    if err := runDoctor(); err != nil {
      return fmt.Errorf("%w\n(pass --skip-doctor to install anyway)", err)
    }
    fmt.Println()
  }

  if err := ensureGroup(defaultGroup); err != nil {
    return err
  }

  self, err := os.Executable()
  if err != nil {
    return err
  }
  if self != installedBinary {
    if err := copyFile(self, installedBinary, 0o755); err != nil {
      return fmt.Errorf("could not install the binary: %w", err)
    }
    fmt.Println("installed " + installedBinary)
  }

  if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
    return err
  }
  if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
    // 0600: this file is where the enrollment key goes, and the dagent group is
    // deliberately given the socket rather than the credentials.
    if err := os.WriteFile(cfgPath, []byte(exampleConfig), 0o600); err != nil {
      return err
    }
    fmt.Println("wrote " + cfgPath)
  }

  unit := fmt.Sprintf(unitTemplate, installedBinary, cfgPath)
  if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
    return err
  }
  fmt.Println("wrote " + unitPath)

  for _, args := range [][]string{
    {"daemon-reload"},
    {"enable", "--now", unitName},
  } {
    out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
    if err != nil {
      return fmt.Errorf("systemctl %v: %v: %s", args, err, out)
    }
  }
  fmt.Println("started " + unitName)

  fmt.Printf(`
Add yourself to the %s group, then log out and back in:

    sudo usermod -aG %s $USER

That is equivalent to passwordless sudo: anyone in this group can start a vm
from any kernel and any rootfs. Only add people you would give root.

    dagent vm list
    systemctl status %s
    journalctl -u %s -f
`, defaultGroup, defaultGroup, unitName, unitName)
  return nil
}

func uninstallCommand() *cli.Command {
  return &cli.Command{
    Name:  "uninstall",
    Usage: "stop and remove the system service",
    Description: "Leaves /var/lib/dagent, the config file and the kernel network state alone. " +
      "Run `dagent netd teardown` first if you want the nftables policy removed too.",
    Action: func(ctx context.Context, cmd *cli.Command) error {
      if os.Geteuid() != 0 {
        return errors.New("uninstall needs root")
      }
      for _, args := range [][]string{
        {"disable", "--now", unitName},
        {"daemon-reload"},
      } {
        if out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput(); err != nil {
          fmt.Printf("systemctl %v: %v: %s\n", args, err, out)
        }
      }
      if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
        return err
      }
      fmt.Println("removed " + unitPath)
      fmt.Println("left alone: " + installedBinary + ", " + configPath + ", the data dir, and the nftables policy")
      return nil
    },
  }
}

const exampleConfig = `# dagent configuration. Mode 0600: this file may hold an enrollment key.
#
# control_url: https://control.example.com
# enrollment_key: paste-once-then-it-is-ignored
# insecure: false

# data_dir: /var/lib/dagent
# socket: /run/dagent/dagent.sock
# group: dagent

network:
  pool: 10.64.0.0/16
  gateway: 10.64.0.1
  # uplink: eth0          # default: whichever interface reaches the internet
  dns: 1.1.1.1
  suricata: false         # true queues vm egress to suricata; it must be running
  queues: 4               # must equal suricata's -q flag count
  # no_docker_compat: false
`

// ensureGroup creates the group if it is missing. groupadd rather than writing
// /etc/group ourselves, so NSS and any directory service stay authoritative.
func ensureGroup(name string) error {
  if _, err := user.LookupGroup(name); err == nil {
    return nil
  }
  out, err := exec.Command("groupadd", "--system", name).CombinedOutput()
  if err != nil {
    return fmt.Errorf("could not create the %s group: %v: %s", name, err, out)
  }
  fmt.Println("created the " + name + " group")
  return nil
}

// copyFile writes through a temporary file and renames, because overwriting a
// running binary in place fails with ETXTBSY.
func copyFile(src, dst string, mode os.FileMode) error {
  in, err := os.Open(src)
  if err != nil {
    return err
  }
  defer in.Close()

  if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
    return err
  }
  tmp, err := os.CreateTemp(filepath.Dir(dst), ".dagent-*")
  if err != nil {
    return err
  }
  tmpName := tmp.Name()
  defer func() { _ = os.Remove(tmpName) }()

  if _, err := io.Copy(tmp, in); err != nil {
    _ = tmp.Close()
    return err
  }
  if err := tmp.Close(); err != nil {
    return err
  }
  if err := os.Chmod(tmpName, mode); err != nil {
    return err
  }
  return os.Rename(tmpName, dst)
}
