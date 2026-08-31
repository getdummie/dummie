package main

import (
	"reflect"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestExposedPorts(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]struct{}
		want []int32
	}{
		{"none", nil, []int32{}},
		{"tcp", map[string]struct{}{"8080/tcp": {}}, []int32{8080}},
		{"bare", map[string]struct{}{"8080": {}}, []int32{8080}},
		{"sorted", map[string]struct{}{"443/tcp": {}, "80/tcp": {}}, []int32{80, 443}},
		{"same port twice", map[string]struct{}{"53/tcp": {}, "53/udp": {}}, []int32{53}},
		{"out of range", map[string]struct{}{"0/tcp": {}, "99999/tcp": {}}, []int32{}},
		{"unparseable", map[string]struct{}{"http/tcp": {}}, []int32{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exposedPorts(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("exposedPorts(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// The marimo image is the shape this feature was written against: a null
// entrypoint, a non-root user, one exposed port and the command in $PORT terms.
func TestOSImageConfigFromMarimo(t *testing.T) {
	got := osImageConfigFrom(v1.Config{
		User:         "appuser",
		Entrypoint:   nil,
		Cmd:          []string{"sh", "-c", "exec marimo edit --no-token -p $PORT --host $HOST"},
		Env:          []string{"PORT=8080", "HOST=0.0.0.0"},
		ExposedPorts: map[string]struct{}{"8080/tcp": {}},
	})

	if got.User != "appuser" {
		t.Errorf("user = %q, want appuser", got.User)
	}
	if len(got.Entrypoint) != 0 {
		t.Errorf("entrypoint = %v, want empty", got.Entrypoint)
	}
	if got.Entrypoint == nil {
		t.Error("entrypoint is nil; a null entrypoint has to reach the database as an empty array")
	}
	if !reflect.DeepEqual(got.ExposedPorts, []int32{8080}) {
		t.Errorf("exposed ports = %v, want [8080]", got.ExposedPorts)
	}
	if len(got.Cmd) != 3 || got.Cmd[0] != "sh" {
		t.Errorf("cmd = %v", got.Cmd)
	}
	if !reflect.DeepEqual(got.Env, []string{"PORT=8080", "HOST=0.0.0.0"}) {
		t.Errorf("env = %v", got.Env)
	}
}

func TestValidateOCIRef(t *testing.T) {
	ok := []string{
		"ghcr.io/marimo-team/marimo:latest-sql",
		"alpine",
		"docker.io/library/debian:13",
		"ghcr.io/x/y@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}
	for _, ref := range ok {
		if err := validateOCIRef(ref); err != nil {
			t.Errorf("validateOCIRef(%q) = %v, want nil", ref, err)
		}
	}
	for _, ref := range []string{"", "  ", "NOT AN IMAGE", "ghcr.io/x:"} {
		if err := validateOCIRef(ref); err == nil {
			t.Errorf("validateOCIRef(%q) = nil, want an error", ref)
		}
	}
}

func TestValidateUserOCIRef(t *testing.T) {
	for _, ref := range []string{"alpine", "ghcr.io/x/y:1", "docker.io/library/debian:13"} {
		if err := validateUserOCIRef(ref); err != nil {
			t.Errorf("validateUserOCIRef(%q) = %v, want nil", ref, err)
		}
	}
	// A host outside the allowlist is the case that matters: it would otherwise
	// make the server fetch from anything reachable from it.
	for _, ref := range []string{
		"registry.internal:5000/x/y:1",
		"169.254.169.254/latest:1",
		"localhost:5000/x:1",
		"NOT AN IMAGE",
	} {
		if err := validateUserOCIRef(ref); err == nil {
			t.Errorf("validateUserOCIRef(%q) = nil, want an error", ref)
		}
	}

	t.Setenv("USER_OSIMAGE_REGISTRIES", "registry.internal:5000")
	if err := validateUserOCIRef("registry.internal:5000/x/y:1"); err != nil {
		t.Errorf("an allowlisted registry was rejected: %v", err)
	}
	if err := validateUserOCIRef("ghcr.io/x/y:1"); err == nil {
		t.Error("ghcr.io was accepted while the allowlist named only one other registry")
	}
}
