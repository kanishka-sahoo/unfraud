package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/kanishka-sahoo/unfraud/internal/llm"
)

func TestNewDriver(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "k")
	cases := []struct {
		name, driver, baseURL string
		want                  string // Go type of the driver, or "error"
	}{
		{"no key falls back to the template", "anthropic", "", "llm.Stub"},
		{"an Anthropic-compatible endpoint needs no key", "anthropic", "http://localhost:4000", "llm.Anthropic"},
		{"openrouter with a key", "openrouter", "", "llm.OpenAI"},
		{"openai needs a base URL", "openai", "", "error"},
		{"openai with a base URL", "openai", "http://localhost:11434/v1", "llm.OpenAI"},
		{"unknown driver", "gemini", "", "error"},
	}
	for _, c := range cases {
		d, _, err := newDriver(log, c.driver, "m", c.baseURL, "low", "", "")
		got := "error"
		switch d.(type) {
		case llm.Stub:
			got = "llm.Stub"
		case llm.Anthropic:
			got = "llm.Anthropic"
		case llm.OpenAI:
			got = "llm.OpenAI"
		}
		if (err != nil) != (c.want == "error") || got != c.want {
			t.Errorf("%s: got %s, err %v", c.name, got, err)
		}
	}
	d, _, _ := newDriver(log, "openrouter", "m", "", "", "0", "")
	if o := d.(llm.OpenAI); o.BaseURL != llm.OpenRouterURL || !o.OpenRouter || *o.Temperature != 0 {
		t.Errorf("openrouter driver %+v", o)
	}
}
