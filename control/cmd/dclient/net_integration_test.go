package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/vishvananda/netns"
)

const (
	testGateway = "10.64.0.1"
	testPool    = "10.64.0.0/16"
)

type testVM struct {
	ns   string
	host string
	peer string
	ip   string
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("DCLIENT_INTEGRATION") == "" {
		t.Skip("set DCLIENT_INTEGRATION=1 to run integration tests")
	}
	if os.Geteuid() != 0 {
		t.Skip("integration tests need root")
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func reaches(ns, target string) bool {
	return exec.Command("ip", "netns", "exec", ns, "ping", "-c", "1", "-W", "1", target).Run() == nil
}

func setupFleet(t *testing.T, n int) []testVM {
	t.Helper()

	runtime.LockOSThread()
	orig, err := netns.Get()
	if err != nil {
		t.Fatalf("could not read the current namespace: %v", err)
	}
	fleet, err := netns.New()
	if err != nil {
		t.Fatalf("could not create a namespace: %v", err)
	}
	t.Cleanup(func() {
		_ = netns.Set(orig)
		_ = fleet.Close()
		_ = orig.Close()
		runtime.UnlockOSThread()
	})

	if err := enableForwarding(); err != nil {
		t.Fatalf("could not enable forwarding: %v", err)
	}

	var vms []testVM
	for i := 0; i < n; i++ {
		v := testVM{
			ns:   fmt.Sprintf("dclient-test-%d", i),
			host: fmt.Sprintf("dvm-test%d", i),
			peer: fmt.Sprintf("dvm-peer%d", i),
			ip:   fmt.Sprintf("10.64.0.%d", i+2),
		}

		_ = exec.Command("ip", "netns", "delete", v.ns).Run()
		run(t, "ip", "netns", "add", v.ns)
		run(t, "ip", "link", "add", v.host, "type", "veth", "peer", "name", v.peer)
		run(t, "ip", "link", "set", v.peer, "netns", v.ns)

		run(t, "ip", "netns", "exec", v.ns, "ip", "addr", "add", v.ip+"/32", "dev", v.peer)
		run(t, "ip", "netns", "exec", v.ns, "ip", "link", "set", v.peer, "up")
		run(t, "ip", "netns", "exec", v.ns, "ip", "route", "add", testGateway, "dev", v.peer, "scope", "link")
		run(t, "ip", "netns", "exec", v.ns, "ip", "route", "add", "default", "via", testGateway)

		if err := configureTap(v.host, v.ip, testGateway); err != nil {
			t.Fatalf("could not configure %s: %v", v.host, err)
		}

		t.Cleanup(func() {
			_ = exec.Command("ip", "netns", "delete", v.ns).Run()
		})
		vms = append(vms, v)
	}

	cfg := netConfig{Pool: testPool, Gateway: testGateway, Uplink: "lo"}
	if err := applyBaseRuleset(cfg); err != nil {
		t.Fatalf("could not apply the ruleset: %v", err)
	}
	for _, v := range vms {
		record := vm{ID: v.ns, Net: &vmNet{Tap: v.host, IP: v.ip, Gateway: testGateway}}
		if err := addVMPolicy(record); err != nil {
			t.Fatalf("could not admit %s to the policy: %v", v.ip, err)
		}
	}
	return vms
}

func TestIntegrationVMsCannotReachEachOther(t *testing.T) {
	requireIntegration(t)
	vms := setupFleet(t, 3)

	for _, v := range vms {
		if !reaches(v.ns, testGateway) {
			t.Fatalf("%s cannot reach its gateway; the test setup is broken, not the policy", v.ip)
		}
	}

	for _, from := range vms {
		for _, to := range vms {
			if from.ip == to.ip {
				continue
			}
			if reaches(from.ns, to.ip) {
				t.Errorf("%s reached %s; VM-to-VM traffic is not supposed to be possible", from.ip, to.ip)
			}
		}
	}
}

func TestIntegrationIsolationSurvivesAPermissiveRule(t *testing.T) {
	requireIntegration(t)
	vms := setupFleet(t, 2)

	run(t, "nft", "add", "table", "inet", "permissive")
	run(t, "nft", "add", "chain", "inet", "permissive", "forward",
		"{ type filter hook forward priority -100 ; policy accept ; }")
	run(t, "nft", "add", "rule", "inet", "permissive", "forward", "accept")
	t.Cleanup(func() { _ = exec.Command("nft", "delete", "table", "inet", "permissive").Run() })

	if !reaches(vms[0].ns, testGateway) {
		t.Fatal("the permissive rule broke the test setup")
	}
	if reaches(vms[0].ns, vms[1].ip) {
		t.Errorf("%s reached %s once a permissive rule existed; isolation depends on nothing else being wrong",
			vms[0].ip, vms[1].ip)
	}
}

func TestIntegrationEgressIsDeniedByDefault(t *testing.T) {
	requireIntegration(t)
	vms := setupFleet(t, 1)

	const outside = "192.0.2.10"
	if reaches(vms[0].ns, outside) {
		t.Errorf("%s reached %s with an empty allowlist", vms[0].ip, outside)
	}

	out, err := exec.Command("nft", "list", "table", "inet", nftTable).CombinedOutput()
	if err != nil {
		t.Fatalf("could not read the ruleset back: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "policy drop") {
		t.Errorf("the forward chain is not default-deny:\n%s", out)
	}
}
