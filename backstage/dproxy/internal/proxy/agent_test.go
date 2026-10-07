package proxy

import (
	"net/http"
	"net/url"
	"testing"

	"dproxy/internal/control"
)

func TestConsoleHostKindPicksAgentByPath(t *testing.T) {
	cc := &ConsoleConfig{}
	req := func(path string) *http.Request { return &http.Request{URL: &url.URL{Path: path}} }

	k, ok := consoleHostKind(cc, req("/agent"))
	if !ok || k.name != "agent" || k.audPrefix != agentAudPrefix || k.acceptType != control.TypeAgentAccept {
		t.Fatalf("/agent gave %+v", k)
	}
	if k.port != consoleSSHPort || !k.perVMRemoteUser {
		t.Fatalf("the agent must land where the console does: %+v", k)
	}
	for _, p := range []string{"/", "/agent/", "/agents"} {
		if k, _ := consoleHostKind(cc, req(p)); k.name != "console" {
			t.Errorf("%s gave %s, want console", p, k.name)
		}
	}
	if k, _ := consoleHostKind(cc, nil); k.name != "console" {
		t.Errorf("an unparsable request gave %s, want console", k.name)
	}
	if _, ok := consoleHostKind(nil, req("/agent")); ok {
		t.Error("the agent was offered with the console off")
	}
}
