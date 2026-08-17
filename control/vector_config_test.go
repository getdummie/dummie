package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The rendered config has to be parseable yaml, and nothing else in the pipeline
// checks that: it is written from a template with substituted placeholders, pushed
// to every host in the fleet, and the first thing that notices a broken one is
// vector refusing to start -- which stops event shipping everywhere at once.
//
// One doubled quote from a value that was substituted into quotes the template
// already had is enough to do it, and it is invisible in a diff of the template.
func TestRenderVectorConfigIsValidYAML(t *testing.T) {
	for _, tc := range []struct{ name, url, user, password string }{
		{"plain", "http://10.0.0.1:8123", "dummie", "dummie"},
		// Every one of these would break the document if it were interpolated raw
		// rather than quoted, and a password is whatever an operator typed.
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

// Every placeholder has to be substituted. One left behind is not a parse error --
// __DNS_TABLE__ is a perfectly good yaml scalar -- so it would ship, and vector
// would write to a table of that name until somebody wondered where the rows went.
func TestRenderVectorConfigLeavesNoPlaceholders(t *testing.T) {
	out := renderVectorConfig("http://10.0.0.1:8123", "dummie", "dummie")
	// The transform writes column names with a double underscore in them, so the
	// marker is the leading one at the start of a word, which is what a placeholder
	// has and alert__action does not.
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

// The two pipelines have to stay connected end to end. A transform nobody feeds or
// a sink reading an input that does not exist is not a yaml error and not a vector
// startup error either -- it is a sink that silently never receives a row.
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
	// Both destinations exist: the suricata events and the resolver's queries.
	for _, want := range []string{vectorClickHouseTable, vectorDNSTable} {
		if !strings.Contains(out, want) {
			t.Errorf("nothing writes to %s:\n%s", want, out)
		}
	}
}
