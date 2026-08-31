package main

import (
	"reflect"
	"testing"
)

func TestValidateImageUser(t *testing.T) {
	for _, ok := range []string{"", "root", "appuser", "1000", "appuser:appgroup", "1000:1000"} {
		if err := validateImageUser(ok); err != nil {
			t.Errorf("validateImageUser(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{"app user", "a:b:c", ":group", "user:", "\tuser"} {
		if err := validateImageUser(bad); err == nil {
			t.Errorf("validateImageUser(%q) was accepted, want a refusal", bad)
		}
	}
}

func TestValidateEnv(t *testing.T) {
	got, err := validateEnv([]string{"PORT=8080", "  ", "HOST=0.0.0.0", "EMPTY=", "SPACED=a b c"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PORT=8080", "HOST=0.0.0.0", "EMPTY=", "SPACED=a b c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("validateEnv = %q, want %q", got, want)
	}

	for _, bad := range []string{"NOTANASSIGNMENT", "=novalue", "HAS SPACE=1"} {
		if _, err := validateEnv([]string{bad}); err == nil {
			t.Errorf("validateEnv(%q) was accepted, want a refusal", bad)
		}
	}
}

func TestTrimArgsDropsBlanks(t *testing.T) {
	got := trimArgs([]string{"sh", "", "-c", "  ", " exec app "})
	want := []string{"sh", "-c", "exec app"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("trimArgs = %q, want %q", got, want)
	}
}
