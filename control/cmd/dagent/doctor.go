package main

import (
  "bufio"
  "context"
  "fmt"
  "os"
  "runtime"
  "slices"
  "strings"

  "github.com/urfave/cli/v3"
)

// result is a check's verdict. warn is reported but does not fail the run.
type result int

const (
  pass result = iota
  warn
  fail
)

func (r result) String() string {
  switch r {
  case pass:
    return "PASS"
  case warn:
    return "WARN"
  default:
    return "FAIL"
  }
}

// check is one preflight test. Add to `checks` to grow the suite -- nothing
// else needs to change.
type check struct {
  name string
  run  func() (result, string)
}

var checks = []check{
  {"os is linux", checkLinux},
  {"distribution is ubuntu or debian", checkDistro},
}

// supportedDistros are the os-release IDs the agent is tested against.
var supportedDistros = []string{"ubuntu", "debian"}

func doctorCommand() *cli.Command {
  return &cli.Command{
    Name:  "doctor",
    Usage: "check that this machine can run the agent",
    Action: func(ctx context.Context, cmd *cli.Command) error {
      return runDoctor()
    },
  }
}

func runDoctor() error {
  width := 0
  for _, c := range checks {
    if len(c.name) > width {
      width = len(c.name)
    }
  }

  failed := 0
  for _, c := range checks {
    res, detail := c.run()
    if res == fail {
      failed++
    }
    fmt.Printf("%-4s  %-*s  %s\n", res, width, c.name, detail)
  }

  if failed > 0 {
    return cli.Exit(fmt.Sprintf("%d check(s) failed", failed), 1)
  }
  return nil
}

func checkLinux() (result, string) {
  if runtime.GOOS != "linux" {
    return fail, fmt.Sprintf("this is %s; the agent only supports linux", runtime.GOOS)
  }
  return pass, "linux/" + runtime.GOARCH
}

func checkDistro() (result, string) {
  rel, err := readOSRelease()
  if err != nil {
    return fail, "could not read /etc/os-release: " + err.Error()
  }

  if slices.Contains(supportedDistros, rel["ID"]) {
    return pass, describeOSRelease(rel)
  }
  // Derivatives (Mint, Pop!_OS, Raspberry Pi OS...) will mostly behave, but
  // they are not what we test against.
  for _, id := range supportedDistros {
    if slices.Contains(strings.Fields(rel["ID_LIKE"]), id) {
      return warn, describeOSRelease(rel) + " (" + id + "-derived, not " + id + ")"
    }
  }
  return fail, describeOSRelease(rel) + " is not ubuntu or debian"
}

func describeOSRelease(rel map[string]string) string {
  name := rel["PRETTY_NAME"]
  if name == "" {
    name = rel["NAME"] + " " + rel["VERSION_ID"]
  }
  if strings.TrimSpace(name) == "" {
    return "unknown distribution"
  }
  return strings.TrimSpace(name)
}

// osReleasePath is a variable so tests can point it elsewhere.
var osReleasePath = "/etc/os-release"

// readOSRelease parses the KEY=value format of os-release(5), stripping the
// optional quoting.
func readOSRelease() (map[string]string, error) {
  f, err := os.Open(osReleasePath)
  if err != nil {
    return nil, err
  }
  defer f.Close()

  rel := map[string]string{}
  sc := bufio.NewScanner(f)
  for sc.Scan() {
    line := strings.TrimSpace(sc.Text())
    if line == "" || strings.HasPrefix(line, "#") {
      continue
    }
    k, v, ok := strings.Cut(line, "=")
    if !ok {
      continue
    }
    v = strings.TrimSpace(v)
    if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
      v = v[1 : len(v)-1]
    }
    rel[strings.TrimSpace(k)] = v
  }
  return rel, sc.Err()
}

// osFacts is what connect reports at enrollment: the distribution id and its
// version, falling back to runtime info when os-release is unavailable.
func osFacts() (osName, osVersion string) {
  osName = runtime.GOOS
  rel, err := readOSRelease()
  if err != nil {
    return osName, ""
  }
  if id := rel["ID"]; id != "" {
    osName = id
  }
  return osName, rel["VERSION_ID"]
}
