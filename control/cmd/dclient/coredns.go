package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	corednsContainer = "coredns"

	defaultCoreDNSImage = "coredns/coredns:1.13.1"

	corednsConfigDir = "/etc/coredns"
	corednsLogDir    = "/var/log/coredns"
)

var corefilePath = filepath.Join(corednsConfigDir, "Corefile")

func ensureCoreDNS(cfg netConfig) {
	if !cfg.Suricata {
		return
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		log.Print("suricata mode is on but docker is not installed; guests cannot resolve anything")
		return
	}

	switch state := containerState(docker, corednsContainer); state {
	case "running":
		drift := corednsDrift(docker)
		if drift == "" {
			return
		}
		log.Printf("the coredns container %s; recreating it", drift)
		if !removeCoreDNS(docker) {
			return
		}
	case "":
	default:
		log.Printf("coredns container is %s; recreating it", state)
		if !removeCoreDNS(docker) {
			return
		}
	}

	for _, dir := range []string{corednsConfigDir, corednsLogDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("could not create %s: %v", dir, err)
			return
		}
	}
	if err := seedCoreDNSConfig(); err != nil {
		log.Printf("could not write the corefile: %v", err)
		return
	}

	args := corednsRunArgs(defaultCoreDNSImage)
	if out, err := exec.Command(docker, args...).CombinedOutput(); err != nil {
		log.Printf("could not start coredns (docker %s): %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		return
	}

	time.Sleep(500 * time.Millisecond)
	if state := containerState(docker, corednsContainer); state != "running" {
		log.Printf("coredns was started but is already %q; run it in the foreground to see why: docker run --rm %s",
			state, strings.Join(corednsForegroundArgs(defaultCoreDNSImage), " "))
		return
	}
	log.Print("started the coredns container")
}

const bootstrapCorefile = `# Written by dclient when this file is missing. It is never rewritten in place:
# once it exists it belongs to whoever owns it, which is normally the control
# server -- it replaces this file wholesale whenever a vm's allowed destinations
# change.
#
# Refuse everything. dclient does not know what any guest may resolve; only the
# control server does. Until it says otherwise, no name resolves.
.:53 {
    template ANY ANY {
        rcode REFUSED
    }
    log
    errors
}
`

func seedCoreDNSConfig() error {
	if _, err := os.Stat(corefilePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(corefilePath, []byte(bootstrapCorefile), 0o644); err != nil {
		return err
	}
	log.Printf("wrote %s (refuse every lookup until the control server sends a corefile)", corefilePath)
	return nil
}

func reloadCoreDNS(ctx context.Context) error {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return errors.New("docker is not installed, so the corefile was saved but not loaded")
	}
	if state := containerState(docker, corednsContainer); state != "running" {
		return fmt.Errorf("the %s container is not running, so the corefile was saved but not loaded", corednsContainer)
	}
	out, err := exec.CommandContext(ctx, docker, "kill", "-s", "SIGUSR1", corednsContainer).CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not reload the corefile: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func corednsDrift(docker string) string {
	var got struct {
		Config struct {
			Image string
			Cmd   []string
		}
	}
	out, err := exec.Command(docker, "inspect", corednsContainer).Output()
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

	if want := corednsCmd(); !slices.Equal(got.Config.Cmd, want) {
		return fmt.Sprintf("was started with %q but this build wants %q",
			strings.Join(got.Config.Cmd, " "), strings.Join(want, " "))
	}
	if got.Config.Image != defaultCoreDNSImage {
		return fmt.Sprintf("is running %s but dclient expects %s", got.Config.Image, defaultCoreDNSImage)
	}
	return ""
}

func removeCoreDNS(docker string) bool {
	if out, err := exec.Command(docker, "rm", "-f", corednsContainer).CombinedOutput(); err != nil {
		log.Printf("could not remove the coredns container: %v: %s", err, strings.TrimSpace(string(out)))
		return false
	}
	return true
}

func corednsCmd() []string {
	return []string{"-conf", corefilePath}
}

func corednsRunArgs(image string) []string {
	args := []string{
		"run", "-d", "--rm",
		"--name", corednsContainer,
	}
	return append(args, corednsForegroundArgs(image)...)
}

func corednsForegroundArgs(image string) []string {
	args := []string{
		"--network", "host",
		"--cap-add", "NET_BIND_SERVICE",
		"-v", corednsConfigDir + ":" + corednsConfigDir,
		"-v", corednsLogDir + ":" + corednsLogDir,
		image,
	}
	return append(args, corednsCmd()...)
}

func stopCoreDNS() string {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return ""
	}
	if containerState(docker, corednsContainer) == "" {
		return ""
	}
	if out, err := exec.Command(docker, "rm", "-f", corednsContainer).CombinedOutput(); err != nil {
		return fmt.Sprintf("could not remove the %s container: %v: %s",
			corednsContainer, err, strings.TrimSpace(string(out)))
	}
	return "removed the " + corednsContainer + " container"
}

func corednsStatus(cfg netConfig) (result, string) {
	if !cfg.Suricata {
		return pass, "suricata mode is off; no resolver to run"
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return fail, "suricata mode is on but docker is not installed"
	}
	switch state := containerState(docker, corednsContainer); state {
	case "running":
		return pass, "the coredns container is running"
	case "":
		return fail, "suricata mode is on but no " + corednsContainer +
			" container exists; dclient starts one on its next reconcile pass"
	default:
		return fail, fmt.Sprintf("the %s container is %s; guests cannot resolve anything", corednsContainer, state)
	}
}
