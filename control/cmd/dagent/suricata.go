package main

import (
  "encoding/json"
  "fmt"
  "log"
  "os"
  "os/exec"
  "path/filepath"
  "slices"
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
    // Running is not the same as running with the right arguments. An operator
    // who edits network.queues and restarts dagent gets a new ruleset queueing
    // to a count the container was never told about, and packets hashed to an
    // unbound queue are dropped -- so the container is replaced rather than left
    // alone. Nothing to do in the overwhelmingly common case where they agree.
    drift := suricataDrift(docker, cfg)
    if drift == "" {
      return
    }
    log.Printf("the suricata container %s; recreating it", drift)
    if !removeSuricata(docker) {
      return
    }
  case "":
    // Not there at all: the usual case on a fresh boot.
  default:
    // Exited, created or dead. --rm normally reaps it, but a container that
    // failed to start can linger and hold the name.
    log.Printf("suricata container is %s; recreating it", state)
    if !removeSuricata(docker) {
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

// localRules is the default egress policy: deny everything, allow ifconfig.io.
//
// It is written against what actually reaches the queues, which is only the
// VM-to-outside direction of each flow -- return traffic is accepted by
// conntrack and Suricata never sees it. So every verdict here is made on a
// to-server packet, and dropping one kills the flow.
//
// That is also why there is no blanket `drop ip`: a TCP SYN carries no
// destination name, so dropping it would kill the connection before the
// ClientHello that proves where it is going ever arrives. The deny is therefore
// split -- ports at the SYN, names at the handshake -- and the pass rules win
// where they overlap, because Suricata evaluates pass before drop regardless of
// sid order.
const localRules = `# Written by dagent on first start. Edits are preserved: dagent only creates
# this file when it is missing, and never rewrites it.
#
# Default deny, with ifconfig.io allowed. $HOME_NET is the vm pool, so these
# rules only ever judge guest traffic; the host's own is not queued.
#
# Only the vm-to-outside direction reaches suricata, so a drop on a matching
# packet ends the flow there.

# --- allowed --------------------------------------------------------------
# pass beats drop in suricata's action order, so these override the denies
# below no matter what sid they carry.
pass dns $HOME_NET any -> any any (msg:"dagent: allow dns for ifconfig.io"; dns.query; content:"ifconfig.io"; nocase; endswith; sid:1000001; rev:1;)
pass tls $HOME_NET any -> any any (msg:"dagent: allow tls to ifconfig.io"; tls.sni; content:"ifconfig.io"; nocase; endswith; sid:1000002; rev:1;)
# No nocase on http.host: the buffer is already normalized to lowercase, and
# suricata 8 rejects the rule outright rather than warning about it.
pass http $HOME_NET any -> any any (msg:"dagent: allow http to ifconfig.io"; http.host; content:"ifconfig.io"; endswith; sid:1000003; rev:1;)

# --- denied ---------------------------------------------------------------
# Everything that is not tcp. This is safe to judge per-packet: a udp or icmp
# packet is self-describing, so there is no handshake to preserve. The dns pass
# above still gets its query out, since pass is evaluated first.
drop ip $HOME_NET any -> any any (msg:"dagent: deny non-tcp"; ip_proto:!tcp; sid:1000010; rev:1;)

# tcp to anything but the web ports, judged at the syn.
drop tcp $HOME_NET any -> any ![80,443] (msg:"dagent: deny tcp to a non-web port"; sid:1000011; rev:1;)

# On the web ports the destination is only knowable once the request is parsed,
# so these fire on the clienthello or the request line -- one packet in, before
# any payload has left the host.
drop tls $HOME_NET any -> any any (msg:"dagent: deny tls to another host"; sid:1000012; rev:1;)
drop http $HOME_NET any -> any any (msg:"dagent: deny http to another host"; sid:1000013; rev:1;)

# A tunnel on 80 or 443 that is neither http nor tls would otherwise match no
# rule at all and pass by default. app-layer-protocol:failed is detection having
# run and come up with nothing, not detection still pending, so this does not
# catch the handshake.
drop tcp $HOME_NET any -> any [80,443] (msg:"dagent: deny non-web traffic on a web port"; app-layer-protocol:failed; sid:1000014; rev:1;)
`

// seedSuricataConfig writes the config and rule files that are missing. Each is
// independent and only ever created, never rewritten: once these files exist
// they are the operator's, which is the whole reason they live on the host
// instead of in the image.
func seedSuricataConfig(cfg netConfig) error {
  rules := filepath.Join(suricataLibDir, "rules")
  if err := os.MkdirAll(rules, 0o755); err != nil {
    return err
  }

  // suricata.rules is suricata-update's output. Seeded empty because it is
  // listed in rule-files and Suricata treats a listed file it cannot open as a
  // startup error -- an empty one means "no signatures yet", not "will not run".
  for _, f := range []struct{ path, body, note string }{
    {filepath.Join(suricataConfigDir, "suricata.yaml"),
      fmt.Sprintf(suricataConfigTemplate, cfg.Pool), "HOME_NET " + cfg.Pool},
    {filepath.Join(rules, "local.rules"), localRules, "default deny, ifconfig.io allowed"},
    {filepath.Join(rules, "suricata.rules"), "", "empty; run suricata-update to fill it"},
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

// suricataDrift compares the running container against what this config would
// start, and describes the difference or returns "" if there is none. Only the
// image and the arguments are checked: those are what a config change moves, and
// the mounts and capabilities are constants in this file.
func suricataDrift(docker string, cfg netConfig) string {
  var got struct {
    Config struct {
      Image string
      Cmd   []string
    }
  }
  out, err := exec.Command(docker, "inspect", suricataContainer).Output()
  if err != nil {
    return "" // Cannot tell, so leave a working container alone.
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
    return fmt.Sprintf("is running %s but dagent expects %s", got.Config.Image, defaultSuricataImage)
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

// suricataCmd is everything after the image name: one -q per queue dagent hands
// packets to. The count is not a preference but a contract with the ruleset --
// traffic hashed to a queue nobody is bound to is dropped, so a mismatch takes
// VM egress down for a fraction of flows.
func suricataCmd(cfg netConfig) []string {
  var cmd []string
  for q := 0; q < int(cfg.Queues); q++ {
    cmd = append(cmd, "-q", strconv.Itoa(q))
  }
  return append(cmd, "-v")
}

// suricataRunArgs builds the docker invocation.
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
  // Shared with the drift check, so what is compared is by construction the
  // same thing that would be started.
  return append(args, suricataCmd(cfg)...)
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
