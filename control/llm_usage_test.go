package main

import "testing"

func TestLLMUsageSourcesKeepHistoricalGlobalRows(t *testing.T) {
	for _, tc := range []struct {
		global bool
		meter  string
	}{
		{true, "openai"},
		{false, "openai@personal"},
	} {
		meter := llmMeterProvider("openai", tc.global)
		if meter != tc.meter {
			t.Errorf("global=%t: meter provider = %q, want %q", tc.global, meter, tc.meter)
		}
		if got := llmUsageProvider(meter); got != "openai" {
			t.Errorf("global=%t: report provider = %q, want openai", tc.global, got)
		}
	}
}

func TestParseUsageSource(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  usageSource
		ok    bool
	}{
		{"", usageGlobal, true},
		{"global", usageGlobal, true},
		{"personal", usagePersonal, true},
		{"all", usageAll, true},
	} {
		got, ok := parseUsageSource(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Errorf("source %q = (%q, %t), want (%q, %t)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUsageSourceForProvider(t *testing.T) {
	for _, tc := range []struct {
		provider string
		want     usageSource
	}{
		{"openai", usageGlobal},
		{"openai@personal", usagePersonal},
	} {
		if got := usageSourceForProvider(tc.provider); got != tc.want {
			t.Errorf("provider %q = %q, want %q", tc.provider, got, tc.want)
		}
	}
}
