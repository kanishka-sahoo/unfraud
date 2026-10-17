package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// Wire is one message as the Messages API takes it. Content is kept as raw
// JSON so a reply's thinking blocks go back exactly as they came.
type Wire struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type Request struct {
	System   string
	Tools    []ToolSpec
	Messages []Wire
}

type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
}

type Reply struct {
	Content    json.RawMessage // the assistant's content blocks
	StopReason string          // end_turn, tool_use, max_tokens, refusal
	Model      string
	Usage      Usage
	CostUSD    float64
}

// Model writes the assistant's next message, streaming its text to onText.
type Model interface {
	Reply(ctx context.Context, req Request, onText func(string)) (Reply, error)
	Name() string
}

// Claude is the assistant's model: Claude through the Messages API, with
// adaptive thinking and tool use (ADR-0022).
type Claude struct {
	Key     string
	Model   string // claude-sonnet-5-5
	BaseURL string
	Effort  string // output_config.effort; empty leaves the model's default
	Options []option.RequestOption
}

func (c Claude) Name() string { return c.Model }

// Prices in USD per million tokens: input, output, cache read, cache write
// (5 minutes), for the cost shown per Investigation.
var claudePrices = map[string][4]float64{
	"claude-sonnet-5-5": {2, 10, 0.20, 2.50},
	"claude-sonnet-5":   {2, 10, 0.20, 2.50},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01, 0.125},
}

func (c Claude) Reply(ctx context.Context, req Request, onText func(string)) (Reply, error) {
	opts := []option.RequestOption{option.WithAPIKey(c.Key), option.WithMaxRetries(2)}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	client := anthropic.NewClient(append(opts, c.Options...)...)
	var tools []anthropic.BetaToolUnionParam
	for _, t := range req.Tools {
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: t.Properties, Required: t.Required},
		}})
	}
	var msgs []anthropic.BetaMessageParam
	for _, m := range req.Messages {
		raw, err := json.Marshal(m)
		if err != nil {
			return Reply{}, err
		}
		msgs = append(msgs, param.Override[anthropic.BetaMessageParam](json.RawMessage(raw)))
	}
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: req.System}},
		Tools:     tools,
		Messages:  msgs,
		Thinking:  anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{}},
		// The system prompt and tools never change, so the cache covers
		// them and, turn by turn, the conversation so far.
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
	}
	if c.Effort != "" {
		params.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(c.Effort)}
	}
	if c.BaseURL == "" && !strings.Contains(c.Model, "haiku") {
		// A declined request is re-served by a fallback model chosen by
		// the API, so analysts aren't left without an answer. Haiku has no
		// server-side fallback.
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}
	stream := client.Beta.Messages.NewStreaming(ctx, params)
	msg := anthropic.BetaMessage{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return Reply{}, err
		}
		if d, ok := ev.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(anthropic.BetaTextDelta); ok && onText != nil {
				onText(t.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			return Reply{}, fmt.Errorf("claude: %d: %w", apierr.StatusCode, err)
		}
		return Reply{}, fmt.Errorf("claude: %w", err)
	}
	p := msg.ToParam()
	content, err := json.Marshal(p.Content)
	if err != nil {
		return Reply{}, err
	}
	r := Reply{Content: content, StopReason: string(msg.StopReason), Model: string(msg.Model), Usage: Usage{
		InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens,
		CacheReadTokens: msg.Usage.CacheReadInputTokens, CacheWriteTokens: msg.Usage.CacheCreationInputTokens,
	}}
	if pr, ok := claudePrices[c.Model]; ok && c.BaseURL == "" {
		u := r.Usage
		r.CostUSD = (float64(u.InputTokens)*pr[0] + float64(u.OutputTokens)*pr[1] +
			float64(u.CacheReadTokens)*pr[2] + float64(u.CacheWriteTokens)*pr[3]) / 1e6
	}
	return r, nil
}

// Stub stands in for Claude without an API key: it looks up the alert or
// card on screen (or the newest blocked alerts), then answers from what the
// lookups said, so the console and its tests run end to end.
type Stub struct{}

func (Stub) Name() string { return "stub" }

func (Stub) Reply(_ context.Context, req Request, onText func(string)) (Reply, error) {
	last := req.Messages[len(req.Messages)-1]
	var blocks []block
	json.Unmarshal(last.Content, &blocks)
	var reply []map[string]any
	if len(blocks) > 0 && blocks[0].Type == "tool_result" {
		// Answer from the lookups' summaries.
		var lines []string
		for _, b := range blocks {
			var r struct {
				Summary string `json:"step_summary"`
			}
			json.Unmarshal(toolResultText(b), &r)
			if r.Summary != "" {
				lines = append(lines, "- "+r.Summary)
			}
		}
		text := "Here is what I found (written from a template: no AI model is set up):\n\n" + strings.Join(lines, "\n")
		onText(text)
		reply = append(reply, map[string]any{"type": "text", "text": text})
	} else {
		var o opening
		first := toolResultText(block{Content: req.Messages[0].Content})
		if _, after, ok := strings.Cut(string(first), "<context>"); ok {
			body, _, _ := strings.Cut(after, "</context>")
			json.Unmarshal([]byte(body), &o)
		}
		ids := map[string]string{"alert_id": o.AlertID, "card": o.Card}
		call := func(name string, in map[string]any) {
			b, _ := json.Marshal(in)
			reply = append(reply, map[string]any{"type": "tool_use", "id": fmt.Sprintf("stub_%d_%d", len(req.Messages), len(reply)),
				"name": name, "input": json.RawMessage(b)})
		}
		switch {
		case ids["alert_id"] != "":
			call("get_alert", map[string]any{"alert_id": ids["alert_id"]})
			if ids["card"] != "" {
				call("card_history", map[string]any{"card": ids["card"]})
			}
		case ids["card"] != "":
			call("card_history", map[string]any{"card": ids["card"]})
			call("card_network", map[string]any{"card": ids["card"]})
		default:
			call("search_alerts", map[string]any{"decision": "block", "outcome": "open", "limit": 5})
		}
	}
	content, _ := json.Marshal(reply)
	stop := "end_turn"
	if strings.Contains(string(content), `"tool_use"`) {
		stop = "tool_use"
	}
	return Reply{Content: content, StopReason: stop, Model: "stub"}, nil
}

// block is a content block, read loosely.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// toolResultText is a tool_result's text content.
func toolResultText(b block) []byte {
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return []byte(s)
	}
	var parts []block
	json.Unmarshal(b.Content, &parts)
	var out []byte
	for _, p := range parts {
		out = append(out, p.Text...)
	}
	return out
}
