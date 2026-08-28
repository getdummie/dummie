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
	"slices"

	"github.com/urfave/cli/v3"
)

const (
	installedBinary = "/usr/local/bin/dclient"
	unitPath        = "/etc/systemd/system/dclient.service"
	unitName        = "dclient.service"
)

// unitTemplate is deliberately plain. The daemon does its own signal handling,
// creates its own runtime directory and drops nothing, so there is little for
// systemd to arrange beyond ordering and restarts.
const unitTemplate = `[Unit]
Description=dclient (microvm host client)
Documentation=https://github.com/
After=network-online.target
Wants=network-online.target
# Docker rewrites the forward chain on start; dclient repairs its own accepts
# afterwards, but starting in this order avoids a window with no vm egress.
# Wants, not Requires: dclient runs fine without docker, it just cannot start the
# suricata container, and losing docker later must not stop the daemon.
After=docker.service
Wants=docker.service

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
		Usage: "install dclient as a system service so the cli does not need sudo",
		Description: "Copies this binary to " + installedBinary + ", creates the " + defaultGroup +
			" group and adds the invoking user to it, writes " + unitPath + ", and starts the daemon.\n\n" +
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

	// Before the checks rather than after, so the report describes the host as
	// this install leaves it instead of warning about something install just fixed.
	if msg, err := ensureKVMAccess(); err != nil {
		fmt.Println("WARNING: " + err.Error() + "; vms will fall back to software emulation")
	} else {
		fmt.Println(msg)
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
		// 0600: this file is where the enrollment key goes, and the dclient group is
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
	fmt.Println(addToGroup(ctx, defaultGroup))

	fmt.Printf(`
Membership of the %s group is equivalent to passwordless sudo: anyone in it can
start a vm from any kernel and any rootfs. Only add people you would give root.

    dclient vm list
    systemctl status %s
    journalctl -u %s -f
`, defaultGroup, unitName, unitName)
	return nil
}

// addToGroup puts the operator who ran the install into the group, which is the
// one step that used to be left as a command to copy. The name comes from
// SUDO_USER: install runs as root, so the identity worth adding is the one that
// invoked sudo rather than the one the process ended up as.
//
// It returns what happened instead of an error. An install driven by a
// provisioning tool has no invoking user at all, and that is not a reason to fail
// an otherwise complete install -- but it is a reason to say so.
func addToGroup(ctx context.Context, group string) string {
	name := os.Getenv("SUDO_USER")
	if name == "" || name == "root" {
		return "no invoking user to add to the " + group + " group; add one with: usermod -aG " + group + " <user>"
	}

	u, err := user.Lookup(name)
	if err != nil {
		return fmt.Sprintf("could not look up %s (%v); add them with: usermod -aG %s %s", name, err, group, name)
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Sprintf("could not look up the %s group: %v", group, err)
	}
	if gids, err := u.GroupIds(); err == nil && slices.Contains(gids, g.Gid) {
		return name + " is already in the " + group + " group"
	}

	// -a is what makes this append. Without it, -G replaces every other group the
	// user is in, which on a single-admin host means locking them out of sudo.
	out, err := exec.CommandContext(ctx, "usermod", "-aG", group, name).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("could not add %s to the %s group (%v: %s); add them by hand",
			name, group, err, out)
	}
	return "added " + name + " to the " + group + " group; log out and back in for it to take effect"
}

func uninstallCommand() *cli.Command {
	return &cli.Command{
		Name:  "uninstall",
		Usage: "stop and remove the system service",
		Description: "Leaves /var/lib/dclient, the config file and the kernel network state alone. " +
			"Run `dclient netd teardown` first if you want the nftables policy removed too.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if os.Geteuid() != 0 {
				return errors.New("uninstall needs root")
			}
			// dclient started the companions, so dclient takes them away; leaving them
			// behind would mean units pointing at a config directory nobody maintains.
			removeManagedServices(ctx)
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

// exampleConfig is written on install and is entirely commented out: every key
// has a default, and every feature's default is off, so the file an operator
// finds is both a complete reference and a host that touches nothing.
const exampleConfig = `# control_url: https://control.example.com
# enrollment_key: paste-once-then-it-is-ignored
# insecure: false
#
# data_dir: /var/lib/dclient
# socket: /run/dclient/dclient.sock
# group: dclient
#
# features:
#   ip_forward: false       # sets net.ipv4.ip_forward; needed for any vm egress
#   kvm_access: false       # chown/chmod /dev/kvm, else vms run under emulation
#   nftables: false         # install and reconcile the packet policy
#   docker_compat: false    # accepts in docker's DOCKER-USER chain; needs nftables
#   suricata: false         # ids container in the egress path; needs nftables
#   dhcp: false             # leases for guests, which is how they get a route
#   metadata: false         # per-vm identity service on the gateway
#
# network:
#   pool: 10.64.0.0/16
#   gateway: 10.64.0.1
#   uplink: eth0            # default: whichever interface reaches the internet
#   dns: 1.1.1.1
#   queues: 4               # the suricata service will spin up the same number of queues
#
# There is nothing here about dpipe, dproxy or vector. Which build of each one this
# host runs is set per host in the control server, on the client's own page, next to
# the version it reports. Leave a version empty there and the host tracks the
# control server's own; set a download_url beside it to run a custom build on this
# machine without cutting a release for it.
#
# A connect installs whatever this host is missing and nothing else. Replacing a
# binary that is already running is the Upgrade button on that page, per host.
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
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dclient-*")
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
