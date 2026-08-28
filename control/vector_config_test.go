package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderVectorConfigIsValidYAML(t *testing.T) {
	for _, tc := range []struct{ name, url, user, password string }{
		{"plain", "http://10.0.0.1:8123", "dummie", "dummie"},
		{"password with a colon", "http://10.0.0.1:8123", "dummie", "pa:ss word"},
		{"password with a brace", "http://10.0.0.1:8123", "dummie", "{not a map}"},
		{"password with a quote", "http://10.0.0.1:8123", "dummie", `he said "no"`},
		{"empty credentials", "http://10.0.0.1:8123", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := renderVectorConfig(tc.url, tc.user, tc.password)
			var parsed map[string]any
			if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatalf("the rendered config is not valid yaml: %v\n%s", err, out)
			}
			for _, section := range []string{"sources", "transforms", "sinks"} {
				if _, ok := parsed[section]; !ok {
					t.Errorf("the rendered config has no %s section:\n%s", section, out)
				}
			}
		})
	}
}

func TestRenderVectorConfigLeavesNoPlaceholders(t *testing.T) {
	out := renderVectorConfig("http://10.0.0.1:8123", "dummie", "dummie")
	for _, placeholder := range []string{
		"__EVE_LOG__", "__COREDNS_CONTAINER__", "__DNS_MARKER__",
		"__CLICKHOUSE_URL__", "__CLICKHOUSE_USER__", "__CLICKHOUSE_PASSWORD__",
		"__DATABASE__", "__TABLE__", "__DNS_TABLE__",
	} {
		if strings.Contains(out, placeholder) {
			t.Errorf("%s was not substituted", placeholder)
		}
	}
}

func TestRenderVectorConfigWiresBothPipelines(t *testing.T) {
	out := renderVectorConfig("http://10.0.0.1:8123", "dummie", "dummie")

	var parsed struct {
		Sources    map[string]any `yaml:"sources"`
		Transforms map[string]struct {
			Inputs []string `yaml:"inputs"`
		} `yaml:"transforms"`
		Sinks map[string]struct {
			Inputs []string `yaml:"inputs"`
		} `yaml:"sinks"`
	}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("could not parse the rendered config: %v", err)
	}

	named := map[string]bool{}
	for name := range parsed.Sources {
		named[name] = true
	}
	for name := range parsed.Transforms {
		named[name] = true
	}
	for name, tr := range parsed.Transforms {
		for _, in := range tr.Inputs {
			if !named[in] {
				t.Errorf("transform %s reads %q, which is neither a source nor a transform", name, in)
			}
		}
	}
	for name, sink := range parsed.Sinks {
		if len(sink.Inputs) == 0 {
			t.Errorf("sink %s has no inputs", name)
		}
		for _, in := range sink.Inputs {
			if !named[in] {
				t.Errorf("sink %s reads %q, which is neither a source nor a transform", name, in)
			}
		}
	}
	for _, want := range []string{vectorClickHouseTable, vectorDNSTable} {
		if !strings.Contains(out, want) {
			t.Errorf("nothing writes to %s:\n%s", want, out)
		}
	}
}
