package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"control/internal/proto"
)

// suricata.yaml, dpipe.yaml and the Corefile arrive from the control server
// whole, like dproxy.yaml and vector.yaml before them. None is merged: the file
// the server sent is the file the host runs.
//
// All three are more disruptive to apply than proxy is, so all three are written
// only when the content actually differs. The server sends one of each on every
// connect, and it is normal for that to be a no-op.

// localPool is the VM subnet this host was configured with. Reported in the
// hello frame so the server can put it in HOME_NET, which is the one thing in
// suricata.yaml the control plane cannot know.
//
// "" when the config cannot be read: the server treats that as "do not send a
// config" rather than substituting a default, which is the right way round --
// a wrong HOME_NET silently stops every $HOME_NET rule from matching.
func localPool() string {
	cfg, err := loadConfig("")
	if err != nil {
		log.Printf("could not read the dclient config to report the vm pool: %v", err)
		return ""
	}
	return cfg.Network.Pool
}

// applySuricataConfig installs a pushed suricata.yaml and restarts the
// container if it changed.
//
// A restart, not a reload: Suricata reads this file once at start, and
// suricatasc has no equivalent of reload-rules for it. That is expensive --
// every NFQUEUE binding goes away for as long as the process takes to come
// back, and packets hashed to an unbound queue are dropped -- which is exactly
// why an identical file is not written and nothing is restarted for it.
func applySuricataConfig(ctx context.Context, config string) (bool, error) {
	cfg, err := loadConfig("")
	if err != nil {
		return false, fmt.Errorf("could not read the dclient config: %w", err)
	}
	if !cfg.Features.Suricata {
		return false, errors.New("suricata mode is off on this host, so the config was not installed")
	}

	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	path := suricataConfigPath()
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err == nil && string(existing) == config {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
		return false, err
	}

	// Only if something is running. On a host whose container is down, the
	// reconcile pass starts it with the file that was just written, and a restart
	// of nothing would be an error for no reason.
	docker, derr := exec.LookPath("docker")
	if derr != nil {
		return true, nil
	}
	if containerState(docker, suricataContainer) != "running" {
		return true, nil
	}
	if out, rerr := exec.CommandContext(ctx, docker, "restart", suricataContainer).CombinedOutput(); rerr != nil {
		// The file is already down and is what the next start will read, so it is
		// left in place: rolling it back would leave the host enforcing a config
		// the control plane believes it replaced.
		return true, fmt.Errorf("wrote %s but could not restart suricata: %v: %s",
			path, rerr, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// applyCoreDNSConfig installs a pushed Corefile and makes the resolver re-read
// it if it changed.
//
// A signal rather than a restart, which is the one way this differs from the two
// around it: coredns re-reads the file in place, so no lookup fails while it
// happens, and it keeps serving the old policy if the new file does not parse.
// The comparison is still worth making -- the server sends this on every connect
// and every inventory tick -- but it is buying much less here than it does above.
func applyCoreDNSConfig(ctx context.Context, config string) (bool, error) {
	cfg, err := loadConfig("")
	if err != nil {
		return false, fmt.Errorf("could not read the dclient config: %w", err)
	}
	// The same switch as suricata, because the two are one policy: a resolver that
	// answers only allowed names is pointless without a ruleset dropping everything
	// else, and the ruleset is unusable for a guest that cannot resolve what it was
	// granted. With the feature off, writing this would leave a policy on disk that
	// nothing enforces and that takes effect the day someone turns the mode on.
	if !cfg.Features.Suricata {
		return false, errors.New("suricata mode is off on this host, so the corefile was not installed")
	}

	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	existing, err := os.ReadFile(corefilePath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", corefilePath, err)
	}
	if err == nil && string(existing) == config {
		return false, nil
	}
	if err := os.MkdirAll(corednsConfigDir, 0o755); err != nil {
		return false, fmt.Errorf("could not create %s: %w", corednsConfigDir, err)
	}
	if err := writeFileAtomic(corefilePath, []byte(config), 0o644); err != nil {
		return false, err
	}
	if err := reloadCoreDNS(ctx); err != nil {
		// The file is already down and is what the next start will read, so it stays:
		// rolling it back would leave the host resolving by a policy the control
		// plane believes it replaced.
		return true, err
	}
	return true, nil
}

// applyDpipeConfig installs a pushed dpipe.yaml, and the wildcard certificate it
// may name, and replaces dpipe if either changed.
//
// The certificate travels with the config rather than as a job of its own
// because the two have to land in order and jobs have none: dclient runs each in
// its own goroutine, so a config that turns tls on could reach a host before the
// files it points at, and dpipe refuses to start without a usable certificate.
// Same rule as the proxy cookie secret -- key material first, then the file that
// names it.
//
// An unchanged push is a no-op, which matters even now that replacement is a
// handover: the control server sends one on every connect, and a handover per
// connect would be a new process every few seconds.
func applyDpipeConfig(ctx context.Context, config string, certs *proto.DpipeCerts) (bool, error) {
	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	path := serviceConfigPath(dpipeService)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	configChanged := err != nil || string(existing) != config

	// Before the config either way: on a first install the config names the files,
	// and on a renewal the config is byte-identical and the certificate is the only
	// thing that changed.
	certsChanged, err := writeDpipeCerts(certs)
	if err != nil {
		return false, err
	}
	if !configChanged && !certsChanged {
		return false, nil
	}

	if configChanged {
		// The config turns ssh on and names both key files, so it must not land
		// before they exist or the replacement below is a restart loop.
		if err := ensureDpipeKeys(); err != nil {
			return false, err
		}
		if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
			return false, err
		}
	}

	// Same as the proxy config: on a host that has just enrolled this file can
	// arrive before the job that installs dpipe, and that install starts the unit
	// with what is already on disk.
	if !serviceInstalled(dpipeService) {
		log.Printf("wrote %s before %s was installed; it starts with that config", path, dpipeService)
		return true, nil
	}

	if err := reloadService(ctx, dpipeService); err != nil {
		// Everything is already on disk and is what the next start reads. Left in
		// place for the same reason the proxy config is: rolling it back would leave
		// the host serving something the control plane believes it replaced.
		return true, fmt.Errorf("wrote %s but could not replace %s: %w", path, dpipeService, err)
	}
	return true, nil
}

// writeDpipeCerts puts the fleet's wildcard certificate where the pushed
// dpipe.yaml says it is, and reports whether either file changed.
//
// A nil or empty pair leaves whatever is on disk alone rather than removing it.
// It means the control server has no certificate for this host's domain, and the
// config that arrived with it therefore has tls disabled -- so nothing reads
// these files, and deleting them would turn a control server that has not
// finished issuing into an outage on the next start.
func writeDpipeCerts(certs *proto.DpipeCerts) (bool, error) {
	if certs == nil || certs.Cert == "" || certs.Key == "" {
		return false, nil
	}

	// 0700 for the same reason the key directory is: the private key below is what
	// every guest on this host is impersonated with.
	if err := os.MkdirAll(dpipeCertDir, 0o700); err != nil {
		return false, fmt.Errorf("could not create %s: %w", dpipeCertDir, err)
	}
	// MkdirAll leaves an existing directory's mode alone, so this is the repair
	// for one that was created looser -- by an earlier version, or by hand.
	if err := os.Chmod(dpipeCertDir, 0o700); err != nil {
		return false, fmt.Errorf("could not tighten %s: %w", dpipeCertDir, err)
	}

	certChanged, err := writeIfChanged(dpipeCertPath, certs.Cert, 0o644)
	if err != nil {
		return false, err
	}
	// 0600, and never named in an error or a log line on this path.
	keyChanged, err := writeIfChanged(dpipeKeyPath, certs.Key, 0o600)
	if err != nil {
		return false, err
	}
	if certChanged || keyChanged {
		log.Printf("installed a new wildcard certificate in %s", dpipeCertDir)
	}
	return certChanged || keyChanged, nil
}

// writeIfChanged writes content only when it differs from what is there, and
// says whether it wrote. The comparison is what keeps a re-push of an unchanged
// certificate from replacing a running dpipe.
//
// Not writeIfDifferent, which looks like the same function: that one writes with
// os.WriteFile, which truncates in place. A dpipe starting in that window reads
// an empty or half-written key and refuses to come up -- and the window is
// exactly when a replacement is happening, because that is when both run at once.
// This one stages and renames.
func writeIfChanged(path, content string, mode os.FileMode) (bool, error) {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err == nil && string(existing) == content {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(content), mode); err != nil {
		return false, err
	}
	return true, nil
}
