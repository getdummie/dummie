package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"control/internal/proto"
)

var testWildcardCerts = &proto.DpipeCerts{Cert: "cert-pem", Key: "key-pem"}

type parsedDpipeConfig struct {
	SSH struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"ssh"`
	TLS struct {
		Enabled     bool   `yaml:"enabled"`
		DefaultCert string `yaml:"default_cert"`
		DefaultKey  string `yaml:"default_key"`
		MinVersion  string `yaml:"min_version"`
		Certs       []struct {
			SNI string `yaml:"sni"`
		} `yaml:"certs"`
	} `yaml:"tls"`
	Console struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"console"`
}

func parseDpipeConfig(t *testing.T, out string) parsedDpipeConfig {
	t.Helper()
	var got parsedDpipeConfig
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the generated config is not valid yaml (%v):\n%s", err, out)
	}
	return got
}

func TestGenerateDpipeConfigWithoutACertificate(t *testing.T) {
	got := parseDpipeConfig(t, generateDpipeConfig(nil))

	if got.TLS.Enabled {
		t.Error("tls is on for a host with no certificate; dpipe would refuse to start")
	}
	if got.TLS.DefaultCert != "" || got.TLS.DefaultKey != "" {
		t.Errorf("the config names certificate files that were never sent: %+v", got.TLS)
	}
	if !got.SSH.Enabled || !got.Console.Enabled {
		t.Errorf("ssh or the console was turned off along with tls: %+v", got)
	}
}

func TestGenerateDpipeConfigWithACertificate(t *testing.T) {
	got := parseDpipeConfig(t, generateDpipeConfig(testWildcardCerts))

	if !got.TLS.Enabled {
		t.Fatal("tls is off for a host that has a certificate")
	}
	if got.TLS.DefaultCert != dpipeCertFile || got.TLS.DefaultKey != dpipeKeyFile {
		t.Errorf("the config points at %q/%q, but dclient writes %q/%q",
			got.TLS.DefaultCert, got.TLS.DefaultKey, dpipeCertFile, dpipeKeyFile)
	}
	if got.TLS.MinVersion != "1.2" {
		t.Errorf("min_version = %q, want 1.2", got.TLS.MinVersion)
	}
	if !got.SSH.Enabled || !got.Console.Enabled {
		t.Errorf("turning tls on changed something else: %+v", got)
	}
}

func TestGenerateDpipeConfigUsesTheDefaultCertificateNotTheSNITable(t *testing.T) {
	out := generateDpipeConfig(testWildcardCerts)

	if got := parseDpipeConfig(t, out); len(got.TLS.Certs) != 0 {
		t.Errorf("the config populates the sni table, where a wildcard is never matched: %+v", got.TLS.Certs)
	}
	if strings.Contains(out, "sni:") {
		t.Error("the config mentions sni")
	}
}

func TestGenerateDpipeConfigNamesCustomDomainsBySNI(t *testing.T) {
	certs := &proto.DpipeCerts{
		Cert: "cert-pem", Key: "key-pem",
		Named: []proto.DpipeNamedCert{{SNI: "codingcoffee.dev", Cert: "c", Key: "k"}},
	}
	out := generateDpipeConfig(certs)
	got := parseDpipeConfig(t, out)

	if len(got.TLS.Certs) != 1 || got.TLS.Certs[0].SNI != "codingcoffee.dev" {
		t.Fatalf("the sni table does not carry the custom domain: %+v", got.TLS.Certs)
	}
	if got.TLS.DefaultCert != dpipeCertFile {
		t.Errorf("the wildcard stopped being the fallback: %q", got.TLS.DefaultCert)
	}
	certPath, keyPath := dpipeNamedCertPaths("codingcoffee.dev")
	if !strings.Contains(out, certPath) || !strings.Contains(out, keyPath) {
		t.Errorf("the config does not point at the paths dclient writes:\n%s", out)
	}
}

func TestGenerateDpipeConfigServesACustomDomainWithoutAWildcard(t *testing.T) {
	certs := &proto.DpipeCerts{
		Named: []proto.DpipeNamedCert{{SNI: "codingcoffee.dev", Cert: "c", Key: "k"}},
	}
	got := parseDpipeConfig(t, generateDpipeConfig(certs))

	if !got.TLS.Enabled {
		t.Fatal("tls is off for a host that holds a custom domain certificate")
	}
	if got.TLS.DefaultCert != "" || got.TLS.DefaultKey != "" {
		t.Errorf("the config names wildcard files that were never sent: %+v", got.TLS)
	}
}

func TestGenerateDpipeConfigIsStable(t *testing.T) {
	for _, certs := range []*proto.DpipeCerts{nil, testWildcardCerts} {
		if generateDpipeConfig(certs) != generateDpipeConfig(certs) {
			t.Errorf("generateDpipeConfig(%v) is not deterministic", certs)
		}
	}
}
