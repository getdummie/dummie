package main

import (
  "fmt"
  "log"
  "os"
  "os/exec"
  "path/filepath"
  "strconv"
  "strings"
)

// With suricata mode on, the ruleset queues allowed VM egress to NFQUEUE and
// nothing accepts it until Suricata verdicts it. That makes the container part
// of the data path rather than an optional extra: if it is not running, every
// packet hashed to a queue is dropped. So dagent starts it, and the reconciler
// restarts it whenever it is found missing -- the same reasoning as
// ensureDockerCompat, where drift is repaired every pass rather than once.
const (
  suricataContainer = "suricata"

  // Pinned: a Suricata major version can change rule syntax and the queue
  // handling underneath us, and the container is in the path of all VM egress.
  defaultSuricataImage = "jasonish/suricata:8.0.6-amd64"

  // Suricata reads its config and rules from the host and writes logs back, so
  // an operator manages both with the container stopped or running.
  suricataConfigDir = "/etc/suricata"
  suricataLogDir    = "/var/log/suricata"
  suricataLibDir    = "/var/lib/suricata"
)

// ensureSuricata starts the container if suricata mode is on and it is not
// already up. Idempotent and safe to call on every reconcile pass; it shells out
// to docker rather than using the API so that what runs is exactly what an
// operator can reproduce by hand.
//
// Failures are logged, not returned: a host that cannot start the container is
// in a bad state, but taking the daemon down with it would also stop the DHCP
// and metadata services every running VM depends on.
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
    return
  case "":
    // Not there at all: the usual case on a fresh boot.
  default:
    // Exited, created or dead. --rm normally reaps it, but a container that
    // failed to start can linger and hold the name.
    log.Printf("suricata container is %s; recreating it", state)
    if out, err := exec.Command(docker, "rm", "-f", suricataContainer).CombinedOutput(); err != nil {
      log.Printf("could not remove the stale suricata container: %v: %s", err, strings.TrimSpace(string(out)))
      return
    }
  }

  // Created rather than left to docker, which would make them root-owned
  // directories with no note of who wanted them -- and the config dir has to
  // exist before seedSuricataConfig can write into it.
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

// suricataConfigTemplate is the config a fresh host gets. HOME_NET is the VM
// pool, so a rule written against $HOME_NET means "our guests" on every host
// regardless of what pool it was given -- hardcoding the default would silently
// make every VM external on a host that changed it.
//
// Everything else is left to Suricata's built-in defaults; this file only says
// what dagent knows and the defaults cannot.
const suricataConfigTemplate = `%%YAML 1.1
---
# Written by dagent on first start. Edits are preserved: dagent only creates this
# file when it is missing, and never rewrites it.
vars:
  address-groups:
    HOME_NET: "[%s]"

default-rule-path: /var/lib/suricata/rules
rule-files:
  - suricata.rules
  - local.rules
`

// seedSuricataConfig writes the config and its rule files if they are not there.
// Absent rather than overwritten-each-start, because this file is the operator's
// once it exists -- the whole reason it lives on the host and not in the image.
func seedSuricataConfig(cfg netConfig) error {
  path := filepath.Join(suricataConfigDir, "suricata.yaml")
  if _, err := os.Stat(path); err == nil {
    return nil
  } else if !os.IsNotExist(err) {
    return err
  }

  body := fmt.Sprintf(suricataConfigTemplate, cfg.Pool)
  if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
    return err
  }
  log.Printf("wrote %s with HOME_NET %s", path, cfg.Pool)

  // Both files are listed in rule-files, and Suricata treats a listed file it
  // cannot open as a startup error. An empty local.rules is the normal state on
  // a host with no local rules yet; suricata.rules is normally suricata-update's
  // output, and an empty one means "no signatures" rather than "will not start".
  rules := filepath.Join(suricataLibDir, "rules")
  if err := os.MkdirAll(rules, 0o755); err != nil {
    return err
  }
  for _, name := range []string{"suricata.rules", "local.rules"} {
    p := filepath.Join(rules, name)
    if _, err := os.Stat(p); os.IsNotExist(err) {
      if err := os.WriteFile(p, nil, 0o644); err != nil {
        return err
      }
      log.Printf("created empty %s", p)
    }
  }
  return nil
}

// suricataRunArgs builds the docker invocation. The queue flags come from
// cfg.Queues rather than being hardcoded, because a count that disagrees with
// the ruleset's is an outage: packets hashed to an unbound queue are dropped.
func suricataRunArgs(cfg netConfig, image string) []string {
  args := []string{
    "run", "-d", "--rm",
    "--name", suricataContainer,
    // Host networking because the queues are the host's, and these three
    // capabilities are what binding them and setting thread priorities need.
    "--network", "host",
    "--cap-add", "NET_ADMIN",
    "--cap-add", "NET_RAW",
    "--cap-add", "SYS_NICE",
    "-v", suricataConfigDir + ":" + suricataConfigDir,
    "-v", suricataLogDir + ":" + suricataLogDir,
    "-v", suricataLibDir + ":" + suricataLibDir,
    image,
  }
  for q := 0; q < int(cfg.Queues); q++ {
    args = append(args, "-q", strconv.Itoa(q))
  }
  return append(args, "-v")
}

// stopSuricata removes the container, returning what to tell the operator. It
// says nothing at all when there was nothing to stop -- teardown on a host that
// never ran suricata should not mention it.
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

// suricataStatus reports what the doctor should say about the container. Split
// out from ensureSuricata so the check can run as a non-root read without
// starting anything.
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
      " container exists; dagent starts one on its next reconcile pass"
  default:
    return fail, fmt.Sprintf("the %s container is %s; vm egress is being dropped", suricataContainer, state)
  }
}

// containerState returns docker's status word for the container, or "" if there
// is no such container -- which is also what a docker that cannot be reached
// looks like, deliberately: the caller's response to both is to try to start it.
func containerState(docker, name string) string {
  out, err := exec.Command(docker, "inspect", "-f", "{{.State.Status}}", name).Output()
  if err != nil {
    return ""
  }
  return strings.TrimSpace(string(out))
}
