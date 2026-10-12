package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
)

// OpenRouter calls OpenRouter's OpenAI-compatible chat completions API.
type OpenRouter struct {
	Key   string
	Model string // e.g. "anthropic/claude-haiku-4.5"
	// Providers, when set, pins upstream providers in order with fallbacks
	// off, so benchmark runs are comparable (ADR-0009).
	Providers []string
	BaseURL   string // default https://openrouter.ai/api/v1
	Client    *http.Client
}

var summarySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary":    map[string]any{"type": "string"},
		"key_points": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 4},
	},
	"required":             []string{"summary", "key_points"},
	"additionalProperties": false,
}

type chatResponse struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Choices  []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		Cost             float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (o OpenRouter) Summarize(ctx context.Context, pk cases.Packet) (cases.Summary, Meta, error) {
	packet, err := json.MarshalIndent(pk, "", "  ")
	if err != nil {
		return cases.Summary{}, Meta{}, err
	}
	body := map[string]any{
		"model": o.Model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": "Case packet:\n" + string(packet)},
		},
		"temperature": 0,
		"max_tokens":  500,
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "case_summary", "strict": true, "schema": summarySchema},
		},
		"usage": map[string]any{"include": true},
	}
	if len(o.Providers) > 0 {
		body["provider"] = map[string]any{"order": o.Providers, "allow_fallbacks": false}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return cases.Summary{}, Meta{}, err
	}

	start := time.Now()
	resp, err := o.post(ctx, b)
	meta := Meta{Driver: "openrouter", LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		return cases.Summary{}, meta, err
	}
	meta.Model, meta.Provider = resp.Model, resp.Provider
	meta.PromptTokens, meta.CompletionTokens, meta.CostUSD = resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.Cost
	if len(resp.Choices) == 0 {
		return cases.Summary{}, meta, fmt.Errorf("openrouter: no choices")
	}
	var s cases.Summary
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	// Models without strict structured output sometimes wrap JSON in a fence.
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &s); err != nil {
		return cases.Summary{}, meta, fmt.Errorf("openrouter: reply is not the summary JSON: %w", err)
	}
	return s, meta, s.Validate()
}

// post sends the request, retrying rate limits and server errors.
func (o OpenRouter) post(ctx context.Context, body []byte) (*chatResponse, error) {
	base := o.BaseURL
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+o.Key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Title", "unfraud")
		res, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			lastErr = fmt.Errorf("openrouter: %s: %s", res.Status, truncate(raw))
			continue
		}
		var cr chatResponse
		if err := json.Unmarshal(raw, &cr); err != nil {
			return nil, fmt.Errorf("openrouter: %s: %s", res.Status, truncate(raw))
		}
		if res.StatusCode != http.StatusOK || cr.Error != nil {
			msg := string(truncate(raw))
			if cr.Error != nil {
				msg = cr.Error.Message
			}
			return nil, fmt.Errorf("openrouter: %s: %s", res.Status, msg)
		}
		return &cr, nil
	}
	return nil, lastErr
}

func truncate(b []byte) []byte {
	if len(b) > 300 {
		return b[:300]
	}
	return b
}
