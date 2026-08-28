package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	suricataContainer = "suricata"

	defaultSuricataImage = "jasonish/suricata:8.0.6-amd64"

	suricataConfigDir = "/etc/suricata"
	suricataLogDir    = "/var/log/suricata"
	suricataLibDir    = "/var/lib/suricata"
)

func ensureSuricata(cfg netConfig) {
	if !cfg.Suricata {
		return
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		log.Print("suricata mode is on but docker is not installed; all vm egress is being dropped")
		return
	}

	switch state := containerState(docker, suricataContainer); state {
	case "running":
		drift := suricataDrift(docker, cfg)
		if drift == "" {
			return
		}
		log.Printf("the suricata container %s; recreating it", drift)
		if !removeSuricata(docker) {
			return
		}
	case "":
	default:
		log.Printf("suricata container is %s; recreating it", state)
		if !removeSuricata(docker) {
			return
		}
	}

	for _, dir := range []string{suricataConfigDir, suricataLogDir, suricataLibDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("could not create %s: %v", dir, err)
			return
		}
	}
	if err := seedSuricataConfig(cfg); err != nil {
		log.Printf("could not write the suricata config: %v", err)
		return
	}

	args := suricataRunArgs(cfg, defaultSuricataImage)
	if out, err := exec.Command(docker, args...).CombinedOutput(); err != nil {
		log.Printf("could not start suricata (docker %s): %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("started the suricata container on %d queues", cfg.Queues)
}

const suricataConfigTemplate = `%%YAML 1.1
---
# Written by dclient so suricata can start before the control server has sent a
# config. It is replaced wholesale by the one the control server compiles.
vars:
  address-groups:
    HOME_NET: "[%s]"

default-rule-path: /var/lib/suricata/rules
rule-files:
  - local.rules

unix-command:
  enabled: yes
`

const localRules = `# Written by dclient when this file is missing. It is never rewritten in place:
# once it exists it belongs to whoever owns it, which is normally the control
# server -- it replaces this file wholesale whenever a vm's allowed
# destinations change.
#
# Total deny. dclient does not know what any guest is allowed to reach; only the
# control server does. Until it says otherwise, nothing leaves a vm.
#
# $HOME_NET is the vm pool, so this judges guest traffic only -- the host's own
# is never queued. Only the vm-to-outside direction reaches suricata, so this
# drop ends the flow.
drop ip $HOME_NET any -> any any (msg:"dclient: deny all egress (no ruleset from the control server)"; sid:1000000; rev:1;)
`

func seedSuricataConfig(cfg netConfig) error {
	rules := filepath.Join(suricataLibDir, "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		return err
	}

	for _, f := range []struct{ path, body, note string }{
		{suricataConfigPath(),
			fmt.Sprintf(suricataConfigTemplate, cfg.Pool), "bootstrap config, HOME_NET " + cfg.Pool},
		{localRulesPath, localRules, "deny all egress until the control server sends a ruleset"},
	} {
		if _, err := os.Stat(f.path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(f.path, []byte(f.body), 0o644); err != nil {
			return err
		}
		log.Printf("wrote %s (%s)", f.path, f.note)
	}
	return nil
}

var localRulesPath = filepath.Join(suricataLibDir, "rules", "local.rules")

func suricataConfigPath() string { return filepath.Join(suricataConfigDir, "suricata.yaml") }

func applySuricataRules(rules string) error {
	cfg, err := loadConfig("")
	if err != nil {
		return fmt.Errorf("could not read the dclient config: %w", err)
	}
	if !cfg.Features.Suricata {
		return errors.New("suricata mode is off on this host, so the ruleset was not installed")
	}

	if !strings.HasSuffix(rules, "\n") {
		rules += "\n"
	}

	if old, err := os.ReadFile(localRulesPath); err == nil && string(old) == rules {
		return nil
	}

	dir := filepath.Dir(localRulesPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".local.rules.*")
	if err != nil {
		return fmt.Errorf("could not stage the ruleset: %w", err)
	}
	staged := tmp.Name()
	defer os.Remove(staged)

	if _, err := tmp.WriteString(rules); err != nil {
		tmp.Close()
		return fmt.Errorf("could not write the ruleset: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(staged, localRulesPath); err != nil {
		return fmt.Errorf("could not install the ruleset: %w", err)
	}

	return reloadSuricataRules()
}

func reloadSuricataRules() error {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return errors.New("docker is not installed, so the ruleset was saved but not loaded")
	}
	if state := containerState(docker, suricataContainer); state != "running" {
		return fmt.Errorf("the %s container is not running, so the ruleset was saved but not loaded", suricataContainer)
	}
	out, err := exec.Command(docker, "exec", suricataContainer,
		"suricatasc", "-c", "reload-rules").CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not reload the ruleset: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func suricataDrift(docker string, cfg netConfig) string {
	var got struct {
		Config struct {
			Image string
			Cmd   []string
		}
	}
	out, err := exec.Command(docker, "inspect", suricataContainer).Output()
	if err != nil {
		return ""
	}
	var containers []json.RawMessage
	if err := json.Unmarshal(out, &containers); err != nil || len(containers) == 0 {
		return ""
	}
	if err := json.Unmarshal(containers[0], &got); err != nil {
		return ""
	}

	if want := suricataCmd(cfg); !slices.Equal(got.Config.Cmd, want) {
		return fmt.Sprintf("was started with %q but this config wants %q",
			strings.Join(got.Config.Cmd, " "), strings.Join(want, " "))
	}
	if got.Config.Image != defaultSuricataImage {
		return fmt.Sprintf("is running %s but dclient expects %s", got.Config.Image, defaultSuricataImage)
	}
	return ""
}

func removeSuricata(docker string) bool {
	if out, err := exec.Command(docker, "rm", "-f", suricataContainer).CombinedOutput(); err != nil {
		log.Printf("could not remove the suricata container: %v: %s", err, strings.TrimSpace(string(out)))
		return false
	}
	return true
}

func suricataCmd(cfg netConfig) []string {
	var cmd []string
	for q := 0; q < int(cfg.Queues); q++ {
		cmd = append(cmd, "-q", strconv.Itoa(q))
	}
	return append(cmd, "-v")
}

func suricataRunArgs(cfg netConfig, image string) []string {
	args := []string{
		"run", "-d", "--rm",
		"--name", suricataContainer,
		"--network", "host",
		"--cap-add", "NET_ADMIN",
		"--cap-add", "NET_RAW",
		"--cap-add", "SYS_NICE",
		"-v", suricataConfigDir + ":" + suricataConfigDir,
		"-v", suricataLogDir + ":" + suricataLogDir,
		"-v", suricataLibDir + ":" + suricataLibDir,
		image,
	}
	return append(args, suricataCmd(cfg)...)
}

func stopSuricata() string {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return ""
	}
	if containerState(docker, suricataContainer) == "" {
		return ""
	}
	if out, err := exec.Command(docker, "rm", "-f", suricataContainer).CombinedOutput(); err != nil {
		return fmt.Sprintf("could not remove the %s container: %v: %s",
			suricataContainer, err, strings.TrimSpace(string(out)))
	}
	return "removed the " + suricataContainer + " container"
}

func suricataStatus(cfg netConfig) (result, string) {
	if !cfg.Suricata {
		return pass, "suricata mode is off; no container to run"
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return fail, "suricata mode is on but docker is not installed"
	}
	switch state := containerState(docker, suricataContainer); state {
	case "running":
		return pass, "the suricata container is running"
	case "":
		return fail, "suricata mode is on but no " + suricataContainer +
			" container exists; dclient starts one on its next reconcile pass"
	default:
		return fail, fmt.Sprintf("the %s container is %s; vm egress is being dropped", suricataContainer, state)
	}
}

func containerState(docker, name string) string {
	out, err := exec.Command(docker, "inspect", "-f", "{{.State.Status}}", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
