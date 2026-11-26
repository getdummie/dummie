package main

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"control/internal/proto"
)

// statusWait is under the control server's own wait, so a hung docker daemon
// shows as one slow row rather than the whole report going missing.
const statusWait = 6 * time.Second

type statusCheck struct {
	name    string
	version func(context.Context) string
	run     func(netConfig) (result, string)
}

func hostStatus(ctx context.Context, data string) []proto.ComponentStatus {
	cfg, cfgErr := loadNetConfig(data)
	withCfg := func(f func(netConfig) (result, string)) func(netConfig) (result, string) {
		return func(c netConfig) (result, string) {
			if cfgErr != nil {
				return warn, "could not read the network config: " + cfgErr.Error()
			}
			return f(c)
		}
	}
	versionOf := func(name string) func(context.Context) string {
		return func(ctx context.Context) string { return installedServiceVersion(ctx, name) }
	}

	checks := []statusCheck{
		{"dclient", func(context.Context) string { return version }, func(netConfig) (result, string) {
			return pass, "answering the control server"
		}},
		{proxyService, versionOf(proxyService), func(netConfig) (result, string) { return unitStatus(ctx, proxyService) }},
		{dpipeService, versionOf(dpipeService), func(netConfig) (result, string) { return unitStatus(ctx, dpipeService) }},
		{intproxyService, versionOf(intproxyService), func(netConfig) (result, string) { return checkIntproxy() }},
		{"docker", nil, withCfg(dockerStatus)},
		{vectorService, nil, func(netConfig) (result, string) { return checkVector() }},
		{"suricata", nil, withCfg(suricataStatus)},
		{"coredns", nil, withCfg(corednsStatus)},
	}

	ctx, cancel := context.WithTimeout(ctx, statusWait)
	defer cancel()

	out := make([]proto.ComponentStatus, len(checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, c := range checks {
		out[i] = proto.ComponentStatus{
			Name: c.name, State: "fail", Detail: "the check did not finish in " + statusWait.String(),
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, detail := c.run(cfg)
			var ver string
			if c.version != nil {
				ver = c.version(ctx)
			}
			mu.Lock()
			out[i] = proto.ComponentStatus{
				Name: c.name, State: strings.ToLower(res.String()), Detail: detail, Version: ver,
			}
			mu.Unlock()
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]proto.ComponentStatus(nil), out...)
}

func unitStatus(ctx context.Context, name string) (result, string) {
	if !serviceInstalled(name) {
		return fail, name + " is not installed; dclient installs it on its next services config"
	}
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", name+".service").Output()
	switch state := strings.TrimSpace(string(out)); state {
	case "active":
		return pass, name + " is running"
	case "":
		return fail, "systemctl did not report a state for " + name
	default:
		return fail, name + " is " + state
	}
}

func (l *link) reportStatus(ctx context.Context, jobID string) {
	ctx = context.WithoutCancel(ctx)
	l.reply(ctx, jobID, proto.JobResult{
		Kind: proto.KindHostStatus, OK: true, Status: hostStatus(ctx, l.data),
	})
}
