package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"
)

const (
	defaultCPUs      = 1
	defaultMemoryMiB = 1024
	defaultStopWait  = 20 * time.Second
)

func vmCommand() *cli.Command {
	return &cli.Command{
		Name:  "vm",
		Usage: "manage local virtual machines",
		Description: "These talk to the dclient daemon over its unix socket, so they need no sudo -- " +
			"only membership of the " + defaultGroup + " group. When no daemon is running and you are " +
			"root, the work is done in-process instead.",
		Commands: []*cli.Command{
			vmCreateCommand(),
			vmListCommand(),
			vmConsoleCommand(),
			vmStartCommand(),
			vmStopCommand(),
			vmRemoveCommand(),
		},
	}
}

func dataDirFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    "data-dir",
		Usage:   "directory holding images and vm state (in-process runs only)",
		Sources: cli.EnvVars("DCLIENT_DATA_DIR"),
	}
}

func dataDir(cmd *cli.Command) string {
	if d := cmd.String("data-dir"); d != "" {
		return d
	}
	if cfg, err := loadConfig(""); err == nil && cfg.DataDir != "" {
		return cfg.DataDir
	}
	return defaultDataDir()
}

func daemon() (*client, error) {
	cfg, err := loadConfig("")
	if err != nil {
		return nil, err
	}
	return dispatch(cfg.Socket)
}

func vmCreateCommand() *cli.Command {
	return &cli.Command{
		Name:  "create",
		Usage: "download images if needed, then boot a new vm in the background",
		Description: "Images are given as http(s) URLs or as paths that already exist on " +
			"the host. Downloads are cached and shared; each vm gets its own copy-on-write overlay.\n\n" +
			"--rootfs takes a filesystem image; --rootfs-tar takes a tar of a root filesystem, as " +
			"`docker export` produces, and builds the ext4 image from it (cached, keyed on the tar).",
		Flags: []cli.Flag{
			dataDirFlag(),
			&cli.StringFlag{
				Name:  "boot",
				Value: string(bootDirect),
				Usage: "`MODE`: direct (microvm, kernel handed to qemu) or disk (firmware boots a full image)",
			},
			&cli.StringFlag{Name: "name", Usage: "human-readable `NAME` (default: the generated id)"},
			&cli.StringFlag{Name: "kernel", Usage: "direct boot: kernel `URL` or path"},
			&cli.StringFlag{Name: "kernel-sha256", Usage: "expected sha256 of the kernel"},
			&cli.StringFlag{Name: "initrd", Usage: "direct boot: optional initramfs `URL` or path"},
			&cli.StringFlag{Name: "initrd-sha256", Usage: "expected sha256 of the initramfs"},
			&cli.StringFlag{Name: "rootfs", Usage: "direct boot: root filesystem image `URL` or path"},
			&cli.StringFlag{Name: "rootfs-sha256", Usage: "expected sha256 of the root filesystem"},
			&cli.StringFlag{Name: "rootfs-tar", Usage: "direct boot: tar of a root filesystem (`docker export`) to build an ext4 image from"},
			&cli.StringFlag{Name: "rootfs-tar-sha256", Usage: "expected sha256 of the tar"},
			&cli.StringFlag{Name: "rootfs-size", Usage: "`SIZE` of the image built from --rootfs-tar, e.g. 2G (default: 1.5x the tar plus 256M)"},
			&cli.StringFlag{Name: "disk", Usage: "disk boot: bootable disk image `URL` or path"},
			&cli.StringFlag{Name: "disk-sha256", Usage: "expected sha256 of the disk image"},
			&cli.StringFlag{Name: "append", Usage: "direct boot: kernel command `LINE` (default: console, root=/dev/vda)"},
			&cli.StringFlag{Name: "firmware", Usage: "disk boot: -bios firmware `PATH` (default: qemu's own)"},
			&cli.StringFlag{Name: "disk-size", Usage: "grow the disk to `SIZE`, e.g. 10G (default: the image's own size)"},
			&cli.IntFlag{Name: "cpus", Value: defaultCPUs, Usage: "vcpu count, also the cgroup cpu ceiling"},
			&cli.IntFlag{Name: "memory", Value: defaultMemoryMiB, Usage: "guest memory in `MIB`"},
			&cli.BoolFlag{Name: "no-network", Usage: "give the vm no network device at all"},
			&cli.StringFlag{Name: "ip", Usage: "pin the vm to this `ADDRESS` instead of taking the lowest free one"},
			&cli.StringSliceFlag{Name: "egress", Usage: "`CIDR` this vm may reach outbound; repeatable, default deny"},
			&cli.BoolFlag{Name: "egress-any", Usage: "allow any destination in the kernel, leaving egress policy to suricata"},
			&cli.IntFlag{Name: "rate-mbit", Usage: "bandwidth ceiling in each direction, in `MBIT`/s (0 = unlimited)"},
			&cli.IntFlag{Name: "burst-kbit", Usage: "burst allowance in `KBIT` (default: a tenth of a second at --rate-mbit)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			req := createRequest{
				Name:         cmd.String("name"),
				Boot:         cmd.String("boot"),
				Kernel:       cmd.String("kernel"),
				KernelSHA:    cmd.String("kernel-sha256"),
				Initrd:       cmd.String("initrd"),
				InitrdSHA:    cmd.String("initrd-sha256"),
				Rootfs:       cmd.String("rootfs"),
				RootfsSHA:    cmd.String("rootfs-sha256"),
				RootfsTar:    cmd.String("rootfs-tar"),
				RootfsTarSHA: cmd.String("rootfs-tar-sha256"),
				RootfsSize:   cmd.String("rootfs-size"),
				Disk:         cmd.String("disk"),
				DiskSHA:      cmd.String("disk-sha256"),
				DiskSize:     cmd.String("disk-size"),
				Append:       cmd.String("append"),
				Firmware:     cmd.String("firmware"),
				CPUs:         int(cmd.Int("cpus")),
				Memory:       int(cmd.Int("memory")),
				NoNetwork:    cmd.Bool("no-network"),
				IP:           cmd.String("ip"),
				Egress:       cmd.StringSlice("egress"),
				EgressAny:    cmd.Bool("egress-any"),
				RateMbit:     int(cmd.Int("rate-mbit")),
				BurstKbit:    int(cmd.Int("burst-kbit")),
			}

			c, err := daemon()
			if err != nil {
				return err
			}

			var v vm
			if c != nil {
				v, err = c.create(ctx, req, os.Stdout)
			} else {
				v, err = createVM(ctx, dataDir(cmd), req, func(f string, a ...any) {
					fmt.Printf(f+"\n", a...)
				})
			}
			if err != nil {
				return err
			}

			fmt.Printf("  console: dclient vm console %s\n", v.ID)
			if v.Net != nil {
				fmt.Printf("  address: %s on %s (offered over dhcp; the guest needs a dhcp client)\n",
					v.Net.IP, v.Net.Tap)
			}
			return nil
		},
	}
}

func vmListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "list the vms on this machine",
		Flags: []cli.Flag{dataDirFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			c, err := daemon()
			if err != nil {
				return err
			}

			var vms []vmStatus
			if c != nil {
				if vms, err = c.list(ctx); err != nil {
					return err
				}
			} else {
				data := dataDir(cmd)
				local, err := listVMs(data)
				if err != nil {
					return err
				}
				for _, v := range local {
					vms = append(vms, vmStatus{VM: v, PID: vmPID(data, v.ID)})
				}
			}

			if len(vms) == 0 {
				fmt.Println("no vms")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tSTATE\tPID\tADDRESS\tBOOT\tCPUS\tMEM\tCREATED")
			for _, s := range vms {
				state := "stopped"
				if s.PID > 0 {
					state = "running"
				}
				address := "-"
				if s.Net != nil {
					address = s.Net.IP
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%dM\t%s\n",
					s.ID, s.Name, state, dashIfZero(s.PID), address, s.Boot, s.CPUs, s.MemoryMiB,
					s.CreatedAt.Local().Format(time.RFC3339))
			}
			return w.Flush()
		},
	}
}

func dashIfZero(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

func vmConsoleCommand() *cli.Command {
	return &cli.Command{
		Name:      "console",
		Usage:     "attach to a vm's serial console",
		ArgsUsage: "<vm>",
		Flags:     []cli.Flag{dataDirFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ref := cmd.Args().First()
			if strings.TrimSpace(ref) == "" {
				return errors.New("which vm? pass an id or name (see `dclient vm list`)")
			}
			c, err := daemon()
			if err != nil {
				return err
			}
			if c != nil {
				return c.console(ctx, ref)
			}

			data := dataDir(cmd)
			v, err := resolveVM(data, ref)
			if err != nil {
				return err
			}
			if vmPID(data, v.ID) == 0 {
				return fmt.Errorf("vm %s is not running; its console output is in %s",
					v.ID, vmPath(data, v.ID, vmConsoleLog))
			}
			return attachConsole(vmPath(data, v.ID, vmConsoleSock))
		},
	}
}

func vmStartCommand() *cli.Command {
	return &cli.Command{
		Name:      "start",
		Usage:     "boot a vm that exists but is not running",
		ArgsUsage: "<vm>",
		Flags:     []cli.Flag{dataDirFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ref := cmd.Args().First()
			c, err := daemon()
			if err != nil {
				return err
			}
			if c != nil {
				v, err := c.start(ctx, ref, os.Stdout)
				if err != nil {
					return err
				}
				fmt.Printf("vm %s (%s) started\n", v.ID, v.Name)
				return nil
			}

			data := dataDir(cmd)
			v, err := resolveVM(data, ref)
			if err != nil {
				return err
			}
			logf := func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
			_, err = startVM(ctx, data, v, logf)
			return err
		},
	}
}

func vmStopCommand() *cli.Command {
	return &cli.Command{
		Name:      "stop",
		Usage:     "shut a vm down, forcibly if it will not go quietly",
		ArgsUsage: "<vm>",
		Flags: []cli.Flag{
			dataDirFlag(),
			&cli.DurationFlag{Name: "timeout", Value: defaultStopWait, Usage: "how long to wait before killing"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ref := cmd.Args().First()
			c, err := daemon()
			if err != nil {
				return err
			}
			if c != nil {
				if err := c.stop(ctx, ref, cmd.Duration("timeout")); err != nil {
					return err
				}
				fmt.Printf("vm %s stopped\n", ref)
				return nil
			}

			data := dataDir(cmd)
			v, err := resolveVM(data, ref)
			if err != nil {
				return err
			}
			if err := stopVM(ctx, data, v.ID, cmd.Duration("timeout")); err != nil {
				return err
			}
			if err := teardownVMNetwork(v); err != nil {
				log.Printf("could not fully tear down the network for %s: %v", v.ID, err)
			}
			if err := removeCgroup(v.Cgroup); err != nil {
				log.Printf("could not remove cgroup %s: %v", v.Cgroup, err)
			}
			fmt.Printf("vm %s stopped\n", v.ID)
			return nil
		},
	}
}

func vmRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:      "rm",
		Usage:     "stop a vm and delete its state and disk",
		ArgsUsage: "<vm>",
		Flags:     []cli.Flag{dataDirFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ref := cmd.Args().First()
			c, err := daemon()
			if err != nil {
				return err
			}
			if c != nil {
				if err := c.remove(ctx, ref); err != nil {
					return err
				}
				fmt.Printf("vm %s removed\n", ref)
				return nil
			}

			data := dataDir(cmd)
			v, err := resolveVM(data, ref)
			if err != nil {
				return err
			}
			if err := removeVM(ctx, data, v); err != nil {
				return err
			}
			fmt.Printf("vm %s removed\n", v.ID)
			return nil
		},
	}
}

func resolveVM(data, ref string) (vm, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return vm{}, errors.New("which vm? pass an id or name (see `dclient vm list`)")
	}
	if v, err := loadVM(data, ref); err == nil {
		return v, nil
	}

	vms, err := listVMs(data)
	if err != nil {
		return vm{}, err
	}
	var matches []vm
	for _, v := range vms {
		if v.Name == ref || strings.HasPrefix(v.ID, ref) {
			matches = append(matches, v)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return vm{}, fmt.Errorf("no vm matches %q", ref)
	default:
		return vm{}, fmt.Errorf("%q matches %d vms; use a full id", ref, len(matches))
	}
}

func parseSize(flag, s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch unit := s[len(s)-1]; unit {
	case 'K', 'k':
		mult = 1 << 10
	case 'M', 'm':
		mult = 1 << 20
	case 'G', 'g':
		mult = 1 << 30
	case 'T', 't':
		mult = 1 << 40
	default:
		if unit < '0' || unit > '9' {
			return 0, fmt.Errorf("%s %q: unknown unit %q", flag, s, string(unit))
		}
	}
	if mult > 1 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a size", flag, s)
	}
	return n * mult, nil
}

type netOptions struct {
	IP        string
	Egress    []string
	EgressAny bool
	RateMbit  int
	BurstKbit int
}

func setupVMNetwork(data string, v *vm, opts netOptions) (*os.File, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("networking needs root: creating taps and writing nftables rules is privileged (use --no-network to skip)")
	}
	cfg, err := loadNetConfig(data)
	if err != nil {
		return nil, err
	}

	var ip string
	if opts.IP != "" {
		ip, err = reserveIP(data, cfg.Pool, cfg.Gateway, opts.IP)
	} else {
		ip, err = allocateIP(data, cfg.Pool, cfg.Gateway)
	}
	if err != nil {
		return nil, err
	}

	egress := opts.Egress
	if opts.EgressAny {
		egress = append(egress, "0.0.0.0/0")
	}

	v.Net = &vmNet{
		IP:        ip,
		Gateway:   cfg.Gateway,
		MAC:       macForIP(ip),
		Egress:    egress,
		RateMbit:  opts.RateMbit,
		BurstKbit: opts.BurstKbit,
	}
	v.Net.Tap = v.Net.tapName(v.ID)

	return bringUpVMNetwork(cfg, v)
}

func restoreVMNetwork(data string, v *vm) (*os.File, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("networking needs root: creating taps and writing nftables rules is privileged")
	}
	if v.Net == nil {
		return nil, errors.New("this vm was created with no network")
	}
	cfg, err := loadNetConfig(data)
	if err != nil {
		return nil, err
	}
	v.Net.Gateway = cfg.Gateway
	if v.Net.Tap == "" {
		v.Net.Tap = v.Net.tapName(v.ID)
	}
	return bringUpVMNetwork(cfg, v)
}

func bringUpVMNetwork(cfg netConfig, v *vm) (*os.File, error) {
	if err := enableForwarding(); err != nil {
		return nil, err
	}
	if err := ensureRuleset(cfg); err != nil {
		return nil, err
	}
	if !cfg.NoDockerCompat {
		ensureDockerCompat()
	}

	tap, err := createTap(v.Net.Tap)
	if err != nil {
		return nil, err
	}
	if err := configureTap(v.Net.Tap, v.Net.IP, v.Net.Gateway); err != nil {
		tap.Close()
		return nil, err
	}
	if err := addVMPolicy(*v); err != nil {
		tap.Close()
		return nil, err
	}
	if err := applyBandwidth(v.Net); err != nil {
		_ = removeVMPolicy(*v)
		tap.Close()
		return nil, err
	}
	return tap, nil
}

func teardownVMNetwork(v vm) error {
	if v.Net == nil {
		return nil
	}
	var firstErr error
	for _, step := range []func() error{
		func() error { return removeVMPolicy(v) },
		func() error { return removeBandwidth(v.Net) },
		func() error { return removeTap(v.Net.Tap) },
	} {
		if err := step(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
