package main

import (
	"log"
	"os/exec"
	"strings"
)

// Docker sets the iptables FORWARD policy to DROP and accepts only what matches
// its own bridges. Our VM traffic matches none of it, so on a host running
// Docker every packet dies there -- in a different table, before dclient's chain
// is ever reached. A drop in any table is final, so no rule we write can undo
// it; the accept has to go in Docker's own table.
//
// DOCKER-USER is the chain Docker provides for exactly this, evaluated before
// its own rules and never flushed by the daemon.
const dockerUserChain = "DOCKER-USER"

// dockerAcceptRules let VM traffic through Docker's chain in both directions.
// This is not a policy decision -- accepting here cannot widen anything, since
// dclient's own chain still evaluates independently and still drops by default.
var dockerAcceptRules = [][]string{
	{"-i", tapPrefix + "+", "-j", "ACCEPT"},
	{"-o", tapPrefix + "+", "-j", "ACCEPT"},
}

// ensureDockerCompat makes VM traffic survive Docker's FORWARD policy. It is a
// no-op on hosts with no Docker, and idempotent everywhere, so the reconciler
// can call it on every pass -- which is what repairs it after a docker restart
// or a reboot.
func ensureDockerCompat() {
	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return // no iptables, so no Docker rules to work around
	}
	if err := exec.Command(iptables, "-S", dockerUserChain).Run(); err != nil {
		return // chain absent: Docker is not managing this host's forwarding
	}

	for _, rule := range dockerAcceptRules {
		check := append([]string{"-C", dockerUserChain}, rule...)
		if err := exec.Command(iptables, check...).Run(); err == nil {
			continue // already there
		}
		insert := append([]string{"-I", dockerUserChain, "1"}, rule...)
		if out, err := exec.Command(iptables, insert...).CombinedOutput(); err != nil {
			log.Printf("could not add the %s accept rule (%s): %v: %s",
				dockerUserChain, strings.Join(rule, " "), err, strings.TrimSpace(string(out)))
			continue
		}
		log.Printf("added %s %s (docker's FORWARD policy would otherwise drop vm traffic)",
			dockerUserChain, strings.Join(rule, " "))
	}
}

// dockerCompatNeeded reports whether Docker is filtering forwarded traffic and
// our accepts are missing -- the state doctor should complain about.
func dockerCompatNeeded() (needed bool, reason string) {
	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return false, "iptables is not installed; docker is not filtering forwarded traffic"
	}
	if err := exec.Command(iptables, "-S", dockerUserChain).Run(); err != nil {
		return false, "no " + dockerUserChain + " chain; docker is not managing forwarding"
	}
	for _, rule := range dockerAcceptRules {
		check := append([]string{"-C", dockerUserChain}, rule...)
		if err := exec.Command(iptables, check...).Run(); err != nil {
			return true, dockerUserChain + " is missing " + strings.Join(rule, " ") +
				"; docker will drop vm traffic (netd adds this on startup)"
		}
	}
	return false, dockerUserChain + " allows vm traffic"
}
