package main

import (
	"log"
	"os/exec"
	"strings"
)

const dockerUserChain = "DOCKER-USER"

var dockerAcceptRules = [][]string{
	{"-i", tapPrefix + "+", "-j", "ACCEPT"},
	{"-o", tapPrefix + "+", "-j", "ACCEPT"},
}

func ensureDockerCompat() {
	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return
	}
	if err := exec.Command(iptables, "-S", dockerUserChain).Run(); err != nil {
		return
	}

	for _, rule := range dockerAcceptRules {
		check := append([]string{"-C", dockerUserChain}, rule...)
		if err := exec.Command(iptables, check...).Run(); err == nil {
			continue
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
