package main

import "testing"

func TestLookupLLMPrice(t *testing.T) {
	prices := map[string]llmPrice{
		"zai/glm-4.6":            {input: 1},
		"glm-4.5":                {input: 2},
		"openrouter/z-ai/glm-4.7": {input: 3},
		"deepinfra/glm-4.7":       {input: 4},
	}
	for _, tc := range []struct {
		model string
		want  float64
		ok    bool
	}{
		{"glm-4.6", 1, true},
		{"GLM-4.5", 2, true},
		{"glm-4.7", 4, true},
		{"glm-9", 0, false},
	} {
		p, ok := lookupLLMPrice(prices, "zai", tc.model)
		if ok != tc.ok || p.input != tc.want {
			t.Errorf("%s: got %v %v, want %v %v", tc.model, p.input, ok, tc.want, tc.ok)
		}
	}
}

func TestUsageMonth(t *testing.T) {
	m, err := usageMonth("2026-02")
	if err != nil || m.Format("2006-01-02") != "2026-02-01" || m.AddDate(0, 1, 0).Format("2006-01") != "2026-03" {
		t.Fatalf("month = %v, %v", m, err)
	}
	if _, err := usageMonth("2026-13"); err == nil {
		t.Fatal("an impossible month parsed")
	}
}
