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
    Commands: []*cli.Command{
      vmCreateCommand(),
      vmListCommand(),
      vmConsoleCommand(),
      vmStopCommand(),
      vmRemoveCommand(),
    },
  }
}

// dataDirFlag is repeated on every vm subcommand rather than declared once on
// the parent: v3 does not inherit flags down the tree.
func dataDirFlag() cli.Flag {
  return &cli.StringFlag{
    Name:    "data-dir",
    Usage:   "directory holding images and vm state (default /var/lib/dagent, or the user cache dir)",
    Sources: cli.EnvVars("DAGENT_DATA_DIR"),
  }
}

func dataDir(cmd *cli.Command) string {
  if d := cmd.String("data-dir"); d != "" {
    return d
  }
  return defaultDataDir()
}

// --- create -----------------------------------------------------------------

func vmCreateCommand() *cli.Command {
  return &cli.Command{
    Name:  "create",
    Usage: "download images if needed, then boot a new vm in the background",
    Description: "Images are given as http(s) URLs or as paths that already exist on this " +
      "machine. Downloads are cached and shared; each vm gets its own copy-on-write overlay.\n\n" +
      "--rootfs takes a filesystem image; --rootfs-tar takes a tar of a root filesystem, as " +
      "`docker export` produces, and builds the ext4 image from it (cached, keyed on the tar).\n\n" +
      "The vm has no network devices at all -- that lands with the nftables policy layer.",
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
      return runVMCreate(ctx, cmd)
    },
  }
}

func runVMCreate(ctx context.Context, cmd *cli.Command) error {
  data := dataDir(cmd)

  boot, err := parseBootMode(cmd.String("boot"))
  if err != nil {
    return err
  }
  cpus, memory := int(cmd.Int("cpus")), int(cmd.Int("memory"))
  if cpus < 1 {
    return fmt.Errorf("--cpus must be at least 1")
  }
  if memory < 64 {
    return fmt.Errorf("--memory must be at least 64 MiB")
  }

  size, err := parseSize("--disk-size", cmd.String("disk-size"))
  if err != nil {
    return err
  }
  rootfsSize, err := parseSize("--rootfs-size", cmd.String("rootfs-size"))
  if err != nil {
    return err
  }

  kernel := artifact{cmd.String("kernel"), cmd.String("kernel-sha256")}
  initrd := artifact{cmd.String("initrd"), cmd.String("initrd-sha256")}
  rootfs := artifact{cmd.String("rootfs"), cmd.String("rootfs-sha256")}
  rootfsTar := artifact{cmd.String("rootfs-tar"), cmd.String("rootfs-tar-sha256")}
  disk := artifact{cmd.String("disk"), cmd.String("disk-sha256")}

  // The two boot modes take disjoint inputs; accepting the wrong ones silently
  // would mean booting something other than what was asked for.
  var backing artifact
  fromTar := false
  switch boot {
  case bootDirect:
    if kernel.empty() {
      return fmt.Errorf("--kernel is required for %s boot", bootDirect)
    }
    if rootfs.empty() == rootfsTar.empty() {
      return fmt.Errorf("%s boot needs exactly one of --rootfs or --rootfs-tar", bootDirect)
    }
    if !disk.empty() {
      return fmt.Errorf("--disk is for %s boot; use --rootfs", bootDisk)
    }
    backing = rootfs
    if !rootfsTar.empty() {
      backing, fromTar = rootfsTar, true
    }
  case bootDisk:
    if disk.empty() {
      return fmt.Errorf("--disk is required for %s boot", bootDisk)
    }
    if !kernel.empty() || !initrd.empty() || cmd.String("append") != "" {
      return fmt.Errorf("--kernel, --initrd and --append are for %s boot", bootDirect)
    }
    if !rootfs.empty() || !rootfsTar.empty() {
      return fmt.Errorf("--rootfs and --rootfs-tar are for %s boot; use --disk", bootDirect)
    }
    backing = disk
  }

  id, err := newVMID()
  if err != nil {
    return err
  }
  name := cmd.String("name")
  if name == "" {
    name = id
  }

  v := vm{
    ID:        id,
    Name:      name,
    CreatedAt: time.Now().UTC(),
    Boot:      boot,
    CPUs:      cpus,
    MemoryMiB: memory,
    Append:    cmd.String("append"),
    Firmware:  cmd.String("firmware"),
    Disk:      vmPath(data, id, vmOverlayImage),
  }
  if boot == bootDirect && v.Append == "" {
    v.Append = defaultAppend()
  }

  cache := imagesDir(data)
  if v.Backing, err = backing.resolve(ctx, cache); err != nil {
    return err
  }
  if fromTar {
    if v.Backing, err = ext4FromTar(ctx, cache, v.Backing, rootfsSize); err != nil {
      return err
    }
  }
  if boot == bootDirect {
    if v.Kernel, err = kernel.resolve(ctx, cache); err != nil {
      return err
    }
    if !initrd.empty() {
      if v.Initrd, err = initrd.resolve(ctx, cache); err != nil {
        return err
      }
    }
  }

  if err := os.MkdirAll(vmDir(data, id), 0o700); err != nil {
    return err
  }
  // Anything created from here on has to be undone if the boot fails, or the
  // operator is left with a half-made vm and a stale cgroup.
  cleanup := func() {
    _ = removeCgroup(v.Cgroup)
    _ = os.RemoveAll(vmDir(data, id))
  }

  if err := newOverlay(ctx, v.Backing, v.Disk, size); err != nil {
    cleanup()
    return err
  }

  // Networking, in the one order that is safe: interface, then policy, then the
  // guest. The VM must not be able to send a packet before the rules that
  // constrain it are already in the kernel.
  var tap *os.File
  if !cmd.Bool("no-network") {
    tap, err = setupVMNetwork(data, &v, netOptions{
      IP:        cmd.String("ip"),
      Egress:    cmd.StringSlice("egress"),
      EgressAny: cmd.Bool("egress-any"),
      RateMbit:  int(cmd.Int("rate-mbit")),
      BurstKbit: int(cmd.Int("burst-kbit")),
    })
    if err != nil {
      cleanup()
      return err
    }
    defer tap.Close() // ours closes after exec; qemu holds the inherited copy
    cleanup = func() {
      _ = teardownVMNetwork(v)
      _ = removeCgroup(v.Cgroup)
      _ = os.RemoveAll(vmDir(data, id))
    }
    // The guest configures itself over DHCP, which is the only way it can get
    // a usable default route from a /32. Nothing is put on the kernel command
    // line: a static address there would fight the dhcp client for the
    // interface.
  }

  if v.Cgroup, err = setupCgroup(v); err != nil {
    // Running unprivileged is a legitimate development mode; the guest just
    // does not get resource limits, and saying so is better than refusing.
    log.Printf("WARNING: no cpu or memory limits will be applied: %v", err)
    v.Cgroup = ""
  }

  pid, err := launchVM(ctx, data, &v, tap)
  if err != nil {
    cleanup()
    return err
  }

  // Written after the boot so a vm.json on disk always describes something that
  // actually started.
  if err := saveVM(data, v); err != nil {
    _ = stopVM(ctx, data, id, defaultStopWait)
    cleanup()
    return err
  }

  fmt.Printf("vm %s (%s) started, pid %d\n", v.ID, v.Name, pid)
  fmt.Printf("  console: dagent vm console %s\n", v.ID)
  if v.Net != nil {
    fmt.Printf("  address: %s on %s (offered over dhcp; the guest needs a dhcp client)\n",
      v.Net.IP, v.Net.Tap)
  }
  return nil
}

// netOptions is the network half of a create request.
type netOptions struct {
  IP        string // empty means allocate
  Egress    []string
  EgressAny bool
  RateMbit  int
  BurstKbit int
}

// setupVMNetwork allocates an address, creates and configures the tap, admits
// the VM to the nftables policy and applies its bandwidth ceiling. It returns
// the tap's fd for handing to QEMU.
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
    // Recorded as an ordinary allowlist entry rather than a mode flag, so
    // `vm.json` and `nft list` both say plainly what this VM may reach.
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

  if err := enableForwarding(); err != nil {
    return nil, err
  }
  // The ruleset has to exist before the VM is admitted to it; netd may not be
  // running, and a VM must never start against an empty table.
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

// teardownVMNetwork reverses setupVMNetwork exactly. Policy is withdrawn before
// the interface goes, so there is no window where a tap exists unpoliced.
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

// parseSize accepts plain bytes or a K/M/G/T suffix, as qemu-img does.
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

// --- list -------------------------------------------------------------------

func vmListCommand() *cli.Command {
  return &cli.Command{
    Name:  "list",
    Usage: "list the vms on this machine",
    Flags: []cli.Flag{dataDirFlag()},
    Action: func(ctx context.Context, cmd *cli.Command) error {
      data := dataDir(cmd)
      vms, err := listVMs(data)
      if err != nil {
        return err
      }
      if len(vms) == 0 {
        fmt.Println("no vms")
        return nil
      }

      w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
      fmt.Fprintln(w, "ID\tNAME\tSTATE\tPID\tADDRESS\tBOOT\tCPUS\tMEM\tCREATED")
      for _, v := range vms {
        state, pid := "stopped", vmPID(data, v.ID)
        if pid > 0 {
          state = "running"
        }
        address := "-"
        if v.Net != nil {
          address = v.Net.IP
        }
        fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%dM\t%s\n",
          v.ID, v.Name, state, dashIfZero(pid), address, v.Boot, v.CPUs, v.MemoryMiB,
          v.CreatedAt.Local().Format(time.RFC3339))
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

// --- console ----------------------------------------------------------------

func vmConsoleCommand() *cli.Command {
  return &cli.Command{
    Name:      "console",
    Usage:     "attach to a vm's serial console",
    ArgsUsage: "<vm>",
    Flags:     []cli.Flag{dataDirFlag()},
    Action: func(ctx context.Context, cmd *cli.Command) error {
      data := dataDir(cmd)
      v, err := resolveVM(data, cmd.Args().First())
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

// --- stop / remove ----------------------------------------------------------

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
      data := dataDir(cmd)
      v, err := resolveVM(data, cmd.Args().First())
      if err != nil {
        return err
      }
      if err := stopVM(ctx, data, v.ID, cmd.Duration("timeout")); err != nil {
        return err
      }
      if err := teardownVMNetwork(v); err != nil {
        log.Printf("could not fully tear down the network for %s: %v", v.ID, err)
      }
      // The cgroup can only be removed once it is empty, so this belongs here
      // rather than next to the kill.
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
      data := dataDir(cmd)
      v, err := resolveVM(data, cmd.Args().First())
      if err != nil {
        return err
      }
      if err := stopVM(ctx, data, v.ID, defaultStopWait); err != nil {
        return err
      }
      if err := teardownVMNetwork(v); err != nil {
        log.Printf("could not fully tear down the network for %s: %v", v.ID, err)
      }
      if err := removeCgroup(v.Cgroup); err != nil {
        log.Printf("could not remove cgroup %s: %v", v.Cgroup, err)
      }
      // Only the vm's own directory goes: the cached base images are shared.
      if err := os.RemoveAll(vmDir(data, v.ID)); err != nil {
        return err
      }
      fmt.Printf("vm %s removed\n", v.ID)
      return nil
    },
  }
}

// resolveVM accepts an id, a name, or an unambiguous id prefix.
func resolveVM(data, ref string) (vm, error) {
  ref = strings.TrimSpace(ref)
  if ref == "" {
    return vm{}, fmt.Errorf("which vm? pass an id or name (see `dagent vm list`)")
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
