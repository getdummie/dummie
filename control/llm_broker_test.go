package main

import (
	"strings"
	"testing"
)

func TestLLMResourceShape(t *testing.T) {
	for _, ok := range []string{"zai/openai", "zai@global/anthropic", "chatgpt/responses"} {
		if llmResourceRe.FindStringSubmatch(ok) == nil {
			t.Errorf("%q was refused", ok)
		}
	}
	for _, bad := range []string{"", "zai", "zai/", "zai@team/openai", "zai/gemini", "ZAI/openai", "zai/openai/x"} {
		if llmResourceRe.FindStringSubmatch(bad) != nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestLLMPlansResolveToHTTPS(t *testing.T) {
	for _, p := range llmProviders {
		for _, pl := range p.Plans {
			got, ok := llmPlanFor(p.ID, pl.ID)
			if !ok {
				t.Errorf("%s/%s did not resolve", p.ID, pl.ID)
			}
			served := 0
			for format := range llmFormatPaths {
				if b := got.base(format); b != "" {
					served++
					if !strings.HasPrefix(b, "https://") {
						t.Errorf("%s/%s serves %s over %q", p.ID, pl.ID, format, b)
					}
				}
			}
			if served == 0 {
				t.Errorf("%s/%s serves nothing", p.ID, pl.ID)
			}
		}
	}
	if (llmPlan{OpenAIBase: "https://x"}).base("responses") != "" {
		t.Error("a plan without a responses base served one")
	}
	if _, ok := llmPlanFor("zai", "enterprise"); ok {
		t.Error("an unknown plan resolved")
	}
}
