package main

import "testing"

func TestLocalVMProxyPortInBrowserURLs(t *testing.T) {
	t.Setenv("LOCAL_VM_PROXY_PORT", "8080")
	host := "demo.dummie.localhost"

	if got := (&UserHandler{}).proxyURLHost(host); got != host+":8080" {
		t.Fatalf("development VM host = %q, want %q", got, host+":8080")
	}
	if got := (&UserHandler{prod: true}).proxyURLHost(host); got != host {
		t.Fatalf("production VM host = %q, want %q", got, host)
	}
}

func TestLocalVMProxyPortIgnoresInvalidValues(t *testing.T) {
	host := "demo.dummie.localhost"
	for _, port := range []string{"", "0", "65536", "not-a-port"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("LOCAL_VM_PROXY_PORT", port)
			if got := (&UserHandler{}).proxyURLHost(host); got != host {
				t.Fatalf("VM host with port %q = %q, want %q", port, got, host)
			}
		})
	}
}
