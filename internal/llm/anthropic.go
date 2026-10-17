package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
)

// Anthropic calls the Claude Messages API (ADR-0021), or any endpoint that
// speaks it, with structured output so the reply is always the summary JSON.
type Anthropic struct {
	Key   string
	Model string // e.g. "claude-haiku-5-5"
	// BaseURL points at an Anthropic-compatible endpoint; empty is the Claude API.
	BaseURL string
	// Effort is output_config.effort (low, medium, high...); empty leaves the
	// model's default, for endpoints and models that don't take it.
	Effort string
	// Options are extra client options, such as a test server's base URL.
	Options []option.RequestOption
}

// claudeSchema is summarySchema without maxItems, which structured output
// does not accept; Summary.Validate bounds the key points instead.
var claudeSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary":    map[string]any{"type": "string"},
		"key_points": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"context":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"checks":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required":             []string{"summary", "key_points", "context", "checks"},
	"additionalProperties": false,
}

// Prices in USD per million input and output tokens, for the cost metric
// (the Messages API does not report cost). Haiku 5.5's apply to prompts up
// to 100,000 tokens, which Case Packets never exceed.
var anthropicPrices = map[string][2]float64{
	"claude-haiku-5-5": {0.10, 0.50},
	"claude-haiku-4-5": {1, 5},
}

func (a Anthropic) Summarize(ctx context.Context, pk cases.Packet) (cases.Summary, Meta, error) {
	packet, err := json.MarshalIndent(pk, "", "  ")
	if err != nil {
		return cases.Summary{}, Meta{}, err
	}
	opts := []option.RequestOption{option.WithAPIKey(a.Key), option.WithMaxRetries(3)}
	if a.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(a.BaseURL))
	}
	client := anthropic.NewClient(append(opts, a.Options...)...)
	// No temperature: Claude 5 models reject anything but the default.
	// Thinking is adaptive and counts toward max_tokens, so leave it room.
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.Model),
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: SystemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Case packet:\n" + string(packet))),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: claudeSchema},
			Effort: anthropic.OutputConfigEffort(a.Effort),
		},
	}
	start := time.Now()
	msg, err := client.Messages.New(ctx, params)
	meta := Meta{Driver: "anthropic", Provider: a.provider(), LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			return cases.Summary{}, meta, fmt.Errorf("anthropic: %d: %s", apierr.StatusCode, apierr.Error())
		}
		return cases.Summary{}, meta, fmt.Errorf("anthropic: %w", err)
	}
	meta.Model = string(msg.Model)
	meta.PromptTokens, meta.CompletionTokens = int(msg.Usage.InputTokens), int(msg.Usage.OutputTokens)
	if p, ok := anthropicPrices[a.Model]; ok && a.BaseURL == "" {
		meta.CostUSD = (float64(meta.PromptTokens)*p[0] + float64(meta.CompletionTokens)*p[1]) / 1e6
	}
	if msg.StopReason == anthropic.StopReasonRefusal || msg.StopReason == anthropic.StopReasonMaxTokens {
		return cases.Summary{}, meta, fmt.Errorf("anthropic: stopped with %s", msg.StopReason)
	}
	// Thinking blocks come first; the answer is the text.
	var text strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return parseSummary("anthropic", text.String(), meta)
}

func (a Anthropic) provider() string {
	if a.BaseURL == "" {
		return "anthropic"
	}
	return a.BaseURL
}
