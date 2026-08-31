package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// A dev artifact server hands out one stable url whose contents change on every
// build, which is exactly the case "Upgrade now" exists to serve: the marker
// records where a binary came from, not what was in it.
func TestEnsureBinaryForceRefetchesTheSameURL(t *testing.T) {
	body := "build-one"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	old := binDir
	binDir = dir
	defer func() { binDir = old }()

	data := t.TempDir()
	src := srv.URL + "/dpipe"

	replaced, err := ensureBinary(context.Background(), data, dpipeService, src, false)
	if err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatal("the first install did not report a replacement")
	}

	// Unforced and unchanged must stay a no-op, or an ordinary reconnect
	// restarts every service on the host.
	if replaced, err := ensureBinary(context.Background(), data, dpipeService, src, false); err != nil || replaced {
		t.Errorf("an unforced re-install reported replaced=%v (err %v), want false", replaced, err)
	}

	body = "build-two"
	replaced, err = ensureBinary(context.Background(), data, dpipeService, src, true)
	if err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatal("a forced install of the same url was skipped, so Upgrade now does nothing in dev")
	}

	got, err := os.ReadFile(serviceBinary(dpipeService))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "build-two" {
		t.Errorf("the installed binary is %q, want the newly fetched build", got)
	}
}

func TestEnsureBinaryUnforcedLeavesADifferentBuildAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fleet-build"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	old := binDir
	binDir = dir
	defer func() { binDir = old }()

	data := t.TempDir()
	if _, err := ensureBinary(context.Background(), data, dpipeService, srv.URL+"/a", false); err != nil {
		t.Fatal(err)
	}
	// A different url with no force is reported, not acted on: moving a host
	// onto another build stays an explicit decision.
	replaced, err := ensureBinary(context.Background(), data, dpipeService, srv.URL+"/b", false)
	if err != nil {
		t.Fatal(err)
	}
	if replaced {
		t.Error("an unforced install moved the host onto a different build")
	}
}
