package main

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "log"
  "net"
  "os"
  "os/signal"
  "path/filepath"
  "syscall"
  "time"

  "github.com/google/nftables"
  "github.com/urfave/cli/v3"
)

// reconcileInterval is how often kernel state is re-derived from desired state.
// Nothing depends on it for correctness -- create and remove converge
// immediately -- it is there to repair drift from crashes and outside meddling.
const reconcileInterval = 30 * time.Second

const (
  netConfigFile = "net.json"

  // defaultDNS is what guests are told to use. It is deliberately an upstream
  // resolver: dagent does not run one, so pointing guests at the gateway would
  // give them an address that answers nothing.
  defaultDNS = "1.1.1.1"

  // defaultQueues has to match the number of -q flags Suricata is started with.
  // Traffic hashed to a queue nobody is bound to is dropped, so a mismatch is
  // an outage rather than a warning.
  defaultQueues = 4
)

// --- configuration ----------------------------------------------------------

// loadNetConfig reads the host's network settings, filling in defaults on first
// use and persisting them. Both `netd` and `vm create` read this, so the two
// cannot disagree about the pool or the gateway.
func loadNetConfig(data string) (netConfig, error) {
  var cfg netConfig
  b, err := os.ReadFile(filepath.Join(data, netConfigFile))
  switch {
  case err == nil:
    if err := json.Unmarshal(b, &cfg); err != nil {
      return cfg, fmt.Errorf("corrupt %s: %w", netConfigFile, err)
    }
  case !os.IsNotExist(err):
    return cfg, err
  }

  if cfg.Pool == "" {
    cfg.Pool = defaultPool
  }
  if cfg.Gateway == "" {
    cfg.Gateway = defaultGateway
  }
  if cfg.DNS == "" {
    cfg.DNS = defaultDNS
  }
  if cfg.Queues == 0 {
    cfg.Queues = defaultQueues
  }
  if cfg.Uplink == "" {
    if cfg.Uplink, err = defaultUplink(); err != nil {
      return cfg, err
    }
  }
  return cfg, saveNetConfig(data, cfg)
}

func saveNetConfig(data string, cfg netConfig) error {
  if err := os.MkdirAll(data, 0o700); err != nil {
    return err
  }
  b, err := json.MarshalIndent(cfg, "", "  ")
  if err != nil {
    return err
  }
  return os.WriteFile(filepath.Join(data, netConfigFile), b, 0o600)
}

// --- reconciliation ---------------------------------------------------------

// ensureRuleset installs the base table if it is not already present. Cheap
// enough to call on every VM create, which means a VM can never start against
// an empty ruleset just because netd was not running.
func ensureRuleset(cfg netConfig) error {
  c, err := nftables.New()
  if err != nil {
    return err
  }
  tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
  if err != nil {
    return err
  }
  for _, t := range tables {
    if t.Name == nftTable {
      return nil
    }
  }
  return applyBaseRuleset(cfg)
}

// reconcile drives kernel state to match what is on disk. It is a full
// recomputation rather than a diff: the sets are flushed and refilled inside a
// single netlink transaction, so there is no instant at which a running VM has
// no policy, and no way for a stale element to survive.
func reconcile(data string, cfg netConfig) error {
  if err := ensureRuleset(cfg); err != nil {
    return err
  }

  vms, err := listVMs(data)
  if err != nil {
    return err
  }

  var live []vm
  for _, v := range vms {
    if v.Net == nil {
      continue
    }
    if vmPID(data, v.ID) == 0 {
      // Stopped: its tap and shaping are garbage, and its policy must go.
      _ = removeTap(v.Net.Tap)
      _ = removeBandwidth(v.Net)
      continue
    }
    live = append(live, v)
  }

  c, err := nftables.New()
  if err != nil {
    return err
  }
  t := &nftables.Table{Family: nftables.TableFamilyINet, Name: nftTable}

  taps := &nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFName}
  vmIPs := &nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr}
  vmSrc := &nftables.Set{
    Table: t, Name: setVMSrc, Concatenation: true,
    KeyType: nftables.MustConcatSetType(nftables.TypeIFName, nftables.TypeIPAddr),
  }
  egress := &nftables.Set{
    Table: t, Name: setEgress, Concatenation: true, Interval: true,
    KeyType: nftables.MustConcatSetType(nftables.TypeIPAddr, nftables.TypeIPAddr),
  }
  for _, s := range []*nftables.Set{taps, vmIPs, vmSrc, egress} {
    c.FlushSet(s)
  }

  for _, v := range live {
    ip := net.ParseIP(v.Net.IP).To4()
    if ip == nil {
      log.Printf("vm %s has an invalid address %q; skipping", v.ID, v.Net.IP)
      continue
    }
    if err := c.SetAddElements(taps, []nftables.SetElement{{Key: ifname(v.Net.Tap)}}); err != nil {
      return err
    }
    if err := c.SetAddElements(vmIPs, []nftables.SetElement{{Key: ip}}); err != nil {
      return err
    }
    if err := c.SetAddElements(vmSrc, []nftables.SetElement{{Key: append(ifname(v.Net.Tap), ip...)}}); err != nil {
      return err
    }
    elems, err := egressElements(v, ip)
    if err != nil {
      log.Printf("vm %s has an unusable egress rule: %v", v.ID, err)
      continue
    }
    if len(elems) > 0 {
      if err := c.SetAddElements(egress, elems); err != nil {
        return err
      }
    }
  }

  return c.Flush()
}

// --- the daemon -------------------------------------------------------------

func netdCommand() *cli.Command {
  return &cli.Command{
    Name:  "netd",
    Usage: "hold the network policy in the kernel and serve VM metadata",
    Description: "Installs the nftables ruleset, reconciles it against the vms on disk, and " +
      "runs the metadata service. VM creation works without this running -- policy is applied " +
      "inline -- but nothing repairs drift until it is.",
    Flags: []cli.Flag{
      dataDirFlag(),
      &cli.StringFlag{Name: "pool", Usage: "address pool for VMs (default " + defaultPool + ")"},
      &cli.StringFlag{Name: "gateway", Usage: "host address on every tap (default " + defaultGateway + ")"},
      &cli.StringFlag{Name: "uplink", Usage: "interface to masquerade egress out of (default: the default route's)"},
      &cli.StringFlag{Name: "dns", Usage: "resolver `ADDRESS` handed to guests over dhcp (default " + defaultDNS + ")"},
      &cli.BoolFlag{Name: "suricata", Usage: "queue allowed egress to suricata instead of accepting it outright"},
      &cli.IntFlag{Name: "queues", Usage: "nfqueue count; must equal suricata's -q flag count (default 4)"},
    },
    Action: func(ctx context.Context, cmd *cli.Command) error {
      data := dataDir(cmd)
      cfg, err := loadNetConfig(data)
      if err != nil {
        return err
      }
      // Flags override what is on disk, and the override is persisted so the
      // vm subcommands see the same thing.
      if v := cmd.String("pool"); v != "" {
        cfg.Pool = v
      }
      if v := cmd.String("gateway"); v != "" {
        cfg.Gateway = v
      }
      if v := cmd.String("uplink"); v != "" {
        cfg.Uplink = v
      }
      if v := cmd.String("dns"); v != "" {
        if net.ParseIP(v).To4() == nil {
          return fmt.Errorf("--dns %q is not an IPv4 address", v)
        }
        cfg.DNS = v
      }
      if v := int(cmd.Int("queues")); v > 0 {
        cfg.Queues = uint16(v)
      }
      cfg.Suricata = cmd.Bool("suricata")
      if err := saveNetConfig(data, cfg); err != nil {
        return err
      }
      return runNetd(ctx, data, cfg)
    },
  }
}

func runNetd(ctx context.Context, data string, cfg netConfig) error {
  if os.Geteuid() != 0 {
    return errors.New("netd needs root: it writes nftables rules and network interfaces")
  }
  if err := enableForwarding(); err != nil {
    return fmt.Errorf("could not enable ip forwarding: %w", err)
  }

  // Always start from a freshly built table rather than trusting whatever a
  // previous run left behind.
  if err := applyBaseRuleset(cfg); err != nil {
    return err
  }
  log.Printf("policy installed: pool %s, gateway %s, uplink %s", cfg.Pool, cfg.Gateway, cfg.Uplink)

  ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
  defer stop()

  errs := make(chan error, 2)
  meta := &metadataServer{data: data, cfg: cfg}
  go func() { errs <- meta.serve(ctx) }()
  // DHCP is what actually configures the guests: a /32 guest cannot install a
  // default route from an address and netmask alone.
  dhcp := &dhcpServer{data: data, cfg: cfg}
  go func() { errs <- dhcp.serve(ctx) }()

  ticker := time.NewTicker(reconcileInterval)
  defer ticker.Stop()
  for {
    if err := reconcile(data, cfg); err != nil {
      log.Printf("reconcile failed: %v", err)
    }
    select {
    case <-ctx.Done():
      log.Print("shutting down")
      return nil
    case err := <-errs:
      return err
    case <-ticker.C:
    }
  }
}
