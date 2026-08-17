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

// The resolver is the half of the egress policy that gets to answer before any
// packet leaves. Guests are handed the gateway as their dns server, this is what
// listens there, and it answers only the names the control server says a guest
// may reach -- so a destination nobody granted cannot be looked up, and the
// connection to it is never attempted.
//
// That matters because suricata cannot judge a tcp syn: it carries no hostname,
// so the ruleset has to let the handshake to an unknown address complete and can
// only drop the request that follows. Refusing the lookup is what stops the
// handshake from being attempted in the first place.
//
// Like suricata, this container is in the data path: if it is not running, guests
// resolve nothing. So dagent starts it and the reconciler restarts it whenever it
// is found missing.
const (
  corednsContainer = "coredns"

  // Pinned for the same reason the suricata image is: this is in the path of
  // every guest's dns, and the view plugin's expression syntax is not something
  // to have change underneath us.
  defaultCoreDNSImage = "coredns/coredns:1.13.1"

  corednsConfigDir = "/etc/coredns"
  corednsLogDir    = "/var/log/coredns"
)

var corefilePath = filepath.Join(corednsConfigDir, "Corefile")

// ensureCoreDNS starts the container if suricata mode is on and it is not
// already up.
//
// Tied to the same switch as suricata deliberately. The two are one policy: a
// resolver that answers only allowed names is a hole if the ruleset is not also
// dropping everything else, and the ruleset is unusable for a guest that cannot
// resolve the names it was granted. A host running one without the other is in a
// state neither file was written for.
//
// Failures are logged, not returned: a host that cannot start this is in a bad
// state, but taking the daemon down with it would also stop the DHCP and metadata
// services every running VM depends on.
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
    drift := corednsDrift(docker, cfg)
    if drift == "" {
      return
    }
    log.Printf("the coredns container %s; recreating it", drift)
    if !removeCoreDNS(docker) {
      return
    }
  case "":
    // Not there at all: the usual case on a fresh boot.
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

  args := corednsRunArgs(cfg, defaultCoreDNSImage)
  if out, err := exec.Command(docker, args...).CombinedOutput(); err != nil {
    log.Printf("could not start coredns (docker %s): %v: %s",
      strings.Join(args, " "), err, strings.TrimSpace(string(out)))
    return
  }

  // `docker run -d` returning cleanly only means the container was created. A
  // coredns that cannot parse its Corefile or bind its port exits immediately,
  // and with --rm there is nothing left to inspect -- so "started" on its own is
  // a claim this function is not entitled to make. Checking costs one inspect and
  // turns a silent 30-second restart loop into a line saying what happened.
  //
  // The pause is what makes the check mean anything: run -d returns as soon as
  // the container exists, which is before a process that is going to die has
  // died. Long enough to catch a startup failure, short enough that a reconcile
  // pass does not notice.
  time.Sleep(500 * time.Millisecond)
  if state := containerState(docker, corednsContainer); state != "running" {
    log.Printf("coredns was started but is already %q; run it in the foreground to see why: docker run --rm %s",
      state, strings.Join(corednsForegroundArgs(cfg, defaultCoreDNSImage), " "))
    return
  }
  log.Print("started the coredns container")
}

// bootstrapCorefile is what a host resolves with before the control server has
// ever sent it a Corefile: nothing.
//
// The mirror of the bootstrap ruleset in suricata.go, and for the same reason.
// dagent does not know what any guest is allowed to reach; only the control
// server does. A resolver that forwarded everything until told otherwise would
// mean a fresh host, or one whose control link is down, quietly resolving the
// whole internet for its guests.
//
// It still has to start and still has to answer, because a refusal is a
// diagnosable failure and a dead port is not.
const bootstrapCorefile = `# Written by dagent when this file is missing. It is never rewritten in place:
# once it exists it belongs to whoever owns it, which is normally the control
# server -- it replaces this file wholesale whenever a vm's allowed destinations
# change.
#
# Refuse everything. dagent does not know what any guest may resolve; only the
# control server does. Until it says otherwise, no name resolves.
#
# The bind is not optional: on a host running systemd-resolved something already
# holds 127.0.0.53:53, and a wildcard listener cannot share the port with it.
.:53 {
    bind {$DAGENT_GATEWAY}
    template ANY ANY {
        rcode REFUSED
    }
    log
    errors
}
`

// seedCoreDNSConfig writes the bootstrap Corefile if there is none. Only ever
// created, never rewritten: once it exists it is the control server's, or the
// operator's on a host nothing manages.
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

// reloadCoreDNS asks the running container to re-read its Corefile.
//
// SIGUSR1 rather than a restart, for the reason the ruleset is reloaded rather
// than restarted: a restart drops the listener for as long as the process takes
// to come back, and every guest lookup in that window fails. coredns re-reads the
// file in place and keeps serving from the old config if the new one does not
// parse.
//
// A failure here is a real failure and is reported as one. The new file is
// already on disk, so the control plane and the host agree about what should be
// enforced while the running process still enforces the old policy, and only the
// error says so.
func reloadCoreDNS(ctx context.Context) error {
  docker, err := exec.LookPath("docker")
  if err != nil {
    return errors.New("docker is not installed, so the corefile was saved but not loaded")
  }
  if state := containerState(docker, corednsContainer); state != "running" {
    // Not worth failing the job for: the file is on disk and the container reads
    // it at startup. The reconcile pass is what starts it.
    return fmt.Errorf("the %s container is not running, so the corefile was saved but not loaded", corednsContainer)
  }
  out, err := exec.CommandContext(ctx, docker, "kill", "-s", "SIGUSR1", corednsContainer).CombinedOutput()
  if err != nil {
    return fmt.Errorf("could not reload the corefile: %v: %s", err, strings.TrimSpace(string(out)))
  }
  return nil
}

// corednsDrift compares the running container against what this config would
// start. The image, the arguments and the gateway are checked; the mounts and the
// network mode are constants in this file.
//
// The gateway is in there because it is baked into the running process: coredns
// expanded it when it read the Corefile, so an operator who changes it leaves a
// resolver bound to an address the taps no longer use, and every guest lookup
// times out with nothing in any log to say why.
func corednsDrift(docker string, cfg netConfig) string {
  var got struct {
    Config struct {
      Image string
      Cmd   []string
      Env   []string
    }
  }
  out, err := exec.Command(docker, "inspect", corednsContainer).Output()
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

  if want := corednsCmd(); !slices.Equal(got.Config.Cmd, want) {
    return fmt.Sprintf("was started with %q but this build wants %q",
      strings.Join(got.Config.Cmd, " "), strings.Join(want, " "))
  }
  if got.Config.Image != defaultCoreDNSImage {
    return fmt.Sprintf("is running %s but dagent expects %s", got.Config.Image, defaultCoreDNSImage)
  }
  if want := corednsGatewayEnv + "=" + cfg.Gateway; !slices.Contains(got.Config.Env, want) {
    return fmt.Sprintf("was not started with %s", want)
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

// corednsCmd is everything after the image name. The Corefile is named
// explicitly rather than left to the image's working directory, so what the
// container reads is the file this agent manages and not one baked into the
// image.
func corednsCmd() []string {
  return []string{"-conf", corefilePath}
}

// corednsGatewayEnv is the variable the Corefile's bind directive is written
// against. The control server generates that file and does not know this host's
// gateway -- it is told the VM pool in the hello frame and nothing else -- so the
// file says `bind {$DAGENT_GATEWAY}` and the value is supplied here, where the
// gateway is a plain fact from net.json.
//
// coredns expands {$VAR} when it reads the Corefile. Unset, the directive is left
// with no argument and coredns refuses to start, which is the right way round: a
// resolver that fell back to every address is one that fights whatever else the
// host runs on 53.
const corednsGatewayEnv = "DAGENT_GATEWAY"

// corednsRunArgs builds the docker invocation.
//
// Host networking, because the address this has to listen on is the gateway the
// taps see, and a bridged container would answer on an address no guest routes
// to -- and would put docker's NAT between the guest and this process, which
// would rewrite the source address the per-VM views are matched on.
//
// The listener is pinned to the gateway rather than left on 0.0.0.0, and that is
// not about reach: on a host running systemd-resolved there is already a stub
// listener on 127.0.0.53:53, and a wildcard listener cannot share a port with a
// specific-address one on Linux. Left wildcarded, coredns exits at startup on
// every ubuntu or debian host in the fleet. The same collision is why the metadata
// service is not on port 80 -- see metadataPort in nft.go.
func corednsRunArgs(cfg netConfig, image string) []string {
  args := []string{
    "run", "-d", "--rm",
    "--name", corednsContainer,
  }
  return append(args, corednsForegroundArgs(cfg, image)...)
}

// corednsForegroundArgs is everything the two invocations share: the run flags
// that are not about detaching, and the command. Split out so the line
// ensureCoreDNS prints when the container dies is one an operator can paste and
// get the error on their terminal, rather than an approximation of it.
func corednsForegroundArgs(cfg netConfig, image string) []string {
  args := []string{
    "--network", "host",
    "--cap-add", "NET_BIND_SERVICE",
    "-e", corednsGatewayEnv + "=" + cfg.Gateway,
    "-v", corednsConfigDir + ":" + corednsConfigDir,
    "-v", corednsLogDir + ":" + corednsLogDir,
    image,
  }
  // Shared with the drift check, so what is compared is by construction the same
  // thing that would be started.
  return append(args, corednsCmd()...)
}

// stopCoreDNS removes the container, returning what to tell the operator. It says
// nothing when there was nothing to stop.
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

// corednsStatus reports what the doctor should say. Split out from ensureCoreDNS
// so the check can run as a non-root read without starting anything.
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
      " container exists; dagent starts one on its next reconcile pass"
  default:
    return fail, fmt.Sprintf("the %s container is %s; guests cannot resolve anything", corednsContainer, state)
  }
}
