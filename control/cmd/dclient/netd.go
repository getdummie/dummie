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
	"strings"
	"syscall"
	"time"

	"github.com/google/nftables"
	"github.com/urfave/cli/v3"
	"github.com/vishvananda/netlink"
)

const reconcileInterval = 30 * time.Second

const (
	netConfigFile = "net.json"

	defaultDNS = "1.1.1.1"

	defaultQueues = 4
)

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

func reconcile(data string, cfg netConfig) error {
	if err := ensureRuleset(cfg); err != nil {
		return err
	}
	if !cfg.NoDockerCompat {
		ensureDockerCompat()
	}
	ensureSuricata(cfg)
	ensureCoreDNS(cfg)
	if err := ensureIntproxyAddr(); err != nil {
		log.Printf("WARNING: %v", err)
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

	taps := &nftables.Set{Table: t, Name: setTaps, KeyType: nftables.TypeIFIndex}
	vmIPs := &nftables.Set{Table: t, Name: setVMIPs, KeyType: nftables.TypeIPAddr}
	vmSrc := &nftables.Set{
		Table: t, Name: setVMSrc, Concatenation: true,
		KeyType: nftables.MustConcatSetType(nftables.TypeIFIndex, nftables.TypeIPAddr),
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
		idx, err := tapIndex(v.Net.Tap)
		if err != nil {
			log.Printf("vm %s is running but its tap is missing: %v; skipping", v.ID, err)
			continue
		}
		if err := c.SetAddElements(taps, []nftables.SetElement{{Key: idx}}); err != nil {
			return err
		}
		if err := c.SetAddElements(vmIPs, []nftables.SetElement{{Key: ip}}); err != nil {
			return err
		}
		if err := c.SetAddElements(vmSrc, []nftables.SetElement{{Key: srcKey(idx, ip)}}); err != nil {
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
			&cli.StringFlag{Name: "dns", Usage: "resolver `ADDRESS` handed to guests over dhcp when --suricata is off (default " + defaultDNS + "); ignored when it is on, since guests are then pointed at the filtering resolver on the gateway"},
			&cli.BoolFlag{Name: "suricata", Usage: "queue allowed egress to suricata instead of accepting it outright"},
			&cli.IntFlag{Name: "queues", Usage: "nfqueue count; must equal suricata's -q flag count (default 4)"},
			&cli.BoolFlag{Name: "no-docker-compat", Usage: "do not add accept rules for vm traffic to docker's " + dockerUserChain + " chain"},
		},
		Commands: []*cli.Command{netdTeardownCommand()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			data := dataDir(cmd)
			cfg, err := loadNetConfig(data)
			if err != nil {
				return err
			}
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
			cfg.NoDockerCompat = cmd.Bool("no-docker-compat")
			if err := saveNetConfig(data, cfg); err != nil {
				return err
			}
			return runNetd(ctx, data, cfg)
		},
	}
}

func netdTeardownCommand() *cli.Command {
	return &cli.Command{
		Name:  "teardown",
		Usage: "remove the nftables table, taps and shaping device from the kernel",
		Description: "Refuses to run while any vm is still using the network. Stopping netd " +
			"deliberately leaves the policy in the kernel, so this is the only way to take it out.\n\n" +
			"Configuration in net.json and the vm records on disk are left alone; only kernel state goes.",
		Flags: []cli.Flag{dataDirFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runTeardown(dataDir(cmd))
		},
	}
}

func runTeardown(data string) error {
	if os.Geteuid() != 0 {
		return errors.New("teardown needs root: it removes nftables rules and network interfaces")
	}

	vms, err := listVMs(data)
	if err != nil {
		return err
	}
	var running []string
	for _, v := range vms {
		if v.Net != nil && vmPID(data, v.ID) != 0 {
			running = append(running, fmt.Sprintf("%s (%s, %s)", v.ID, v.Name, v.Net.IP))
		}
	}
	if len(running) > 0 {
		return fmt.Errorf("these vms are still on the network; stop them first:\n  %s",
			strings.Join(running, "\n  "))
	}

	if err := deleteRuleset(); err != nil {
		return err
	}
	fmt.Println("removed nftables table inet " + nftTable)

	if msg := stopSuricata(); msg != "" {
		fmt.Println(msg)
	}
	if msg := stopCoreDNS(); msg != "" {
		fmt.Println(msg)
	}

	taps, err := removeStrayTaps()
	if err != nil {
		return err
	}
	for _, name := range taps {
		fmt.Println("removed tap " + name)
	}

	if err := removeTap(ifbDevice); err != nil {
		return fmt.Errorf("could not remove %s: %w", ifbDevice, err)
	}
	fmt.Println("removed " + ifbDevice + " if it existed")

	fmt.Println("note: net.ipv4.ip_forward left enabled, and net.json is unchanged")
	return nil
}

func deleteRuleset() error {
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
			c.DelTable(t)
		}
	}
	return c.Flush()
}

func removeStrayTaps() ([]string, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, l := range links {
		name := l.Attrs().Name
		if !strings.HasPrefix(name, tapPrefix) {
			continue
		}
		if err := netlink.LinkDel(l); err != nil {
			return removed, fmt.Errorf("could not remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func runNetd(ctx context.Context, data string, cfg netConfig) error {
	if os.Geteuid() != 0 {
		return errors.New("netd needs root: it writes nftables rules and network interfaces")
	}
	if err := enableForwarding(); err != nil {
		return fmt.Errorf("could not enable ip forwarding: %w", err)
	}

	if err := applyBaseRuleset(cfg); err != nil {
		return err
	}
	log.Printf("policy installed: pool %s, gateway %s, uplink %s", cfg.Pool, cfg.Gateway, cfg.Uplink)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 2)
	meta := &metadataServer{data: data, cfg: cfg}
	go func() { errs <- meta.serve(ctx) }()
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
