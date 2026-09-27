package main

import "testing"

func TestLLMResourceShape(t *testing.T) {
	for _, ok := range []string{"zai/openai", "zai@global/anthropic"} {
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
			if !ok || got.OpenAIBase[:8] != "https://" || got.AnthropicBase[:8] != "https://" {
				t.Errorf("%s/%s resolves to %+v", p.ID, pl.ID, got)
			}
		}
	}
	if _, ok := llmPlanFor("zai", "enterprise"); ok {
		t.Error("an unknown plan resolved")
	}
}
