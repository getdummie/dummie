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

func TestLocalVMSSHPort(t *testing.T) {
	t.Setenv("LOCAL_VM_SSH_PORT", "2224")
	if got := (&UserHandler{}).vmSSHPort(); got != 2224 {
		t.Fatalf("development SSH port = %d, want 2224", got)
	}
	if got := (&UserHandler{prod: true}).vmSSHPort(); got != 0 {
		t.Fatalf("production SSH port = %d, want default", got)
	}
}

func TestLocalVMSSHPortIgnoresInvalidValues(t *testing.T) {
	for _, port := range []string{"", "0", "65536", "not-a-port"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("LOCAL_VM_SSH_PORT", port)
			if got := (&UserHandler{}).vmSSHPort(); got != 0 {
				t.Fatalf("SSH port with %q = %d, want default", port, got)
			}
		})
	}
}
