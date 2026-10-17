package llm

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
)

// OpenRouterURL is OpenRouter's OpenAI-compatible API.
const OpenRouterURL = "https://openrouter.ai/api/v1"

// OpenAI calls an OpenAI-compatible chat completions API: OpenAI itself, a
// local server such as vLLM or Ollama, or OpenRouter.
type OpenAI struct {
	Key   string
	Model string // e.g. "anthropic/claude-haiku-5.5" on OpenRouter
	// BaseURL is the API root, without /chat/completions.
	BaseURL string
	// OpenRouter turns on OpenRouter's extensions: cost reporting, the
	// X-Title header and provider pinning.
	OpenRouter bool
	// Providers, when set, pins OpenRouter's upstream providers in order
	// with fallbacks off, so benchmark runs are comparable (ADR-0009).
	Providers []string
	// Temperature is sent when set. Benchmark runs pin it to 0 where the
	// model allows; Claude 5 models only accept the default.
	Temperature *float64
	Client      *http.Client
}

var summarySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary":    map[string]any{"type": "string"},
		"key_points": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 4},
		"context":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 4},
		"checks":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 3},
	},
	"required":             []string{"summary", "key_points", "context", "checks"},
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

func (o OpenAI) Summarize(ctx context.Context, pk cases.Packet) (cases.Summary, Meta, error) {
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
		// Room for models that think before answering.
		"max_tokens": 4096,
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "case_summary", "strict": true, "schema": summarySchema},
		},
	}
	if o.Temperature != nil {
		body["temperature"] = *o.Temperature
	}
	if o.OpenRouter {
		body["usage"] = map[string]any{"include": true}
		if len(o.Providers) > 0 {
			body["provider"] = map[string]any{"order": o.Providers, "allow_fallbacks": false}
		}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return cases.Summary{}, Meta{}, err
	}

	start := time.Now()
	resp, err := o.post(ctx, b)
	meta := Meta{Driver: o.driver(), LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		return cases.Summary{}, meta, err
	}
	meta.Model, meta.Provider = resp.Model, cmp.Or(resp.Provider, o.BaseURL)
	meta.PromptTokens, meta.CompletionTokens, meta.CostUSD = resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.Cost
	if len(resp.Choices) == 0 {
		return cases.Summary{}, meta, fmt.Errorf("%s: no choices", meta.Driver)
	}
	return parseSummary(meta.Driver, resp.Choices[0].Message.Content, meta)
}

func (o OpenAI) driver() string {
	if o.OpenRouter {
		return "openrouter"
	}
	return "openai"
}

// parseSummary reads a reply as the summary JSON. Models without strict
// structured output sometimes wrap it in a fence.
func parseSummary(driver, content string, meta Meta) (cases.Summary, Meta, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	var s cases.Summary
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &s); err != nil {
		return cases.Summary{}, meta, fmt.Errorf("%s: reply is not the summary JSON: %w", driver, err)
	}
	// The next step closes the brief, wherever the model put it.
	slices.SortStableFunc(s.KeyPoints, func(a, b string) int {
		return cmp.Compare(btoi(strings.HasPrefix(a, "Check:")), btoi(strings.HasPrefix(b, "Check:")))
	})
	return s, meta, s.Validate()
}

// post sends the request, retrying rate limits and server errors.
func (o OpenAI) post(ctx context.Context, body []byte) (*chatResponse, error) {
	base := strings.TrimSuffix(o.BaseURL, "/")
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
		if o.Key != "" { // local servers often need none
			req.Header.Set("Authorization", "Bearer "+o.Key)
		}
		req.Header.Set("Content-Type", "application/json")
		if o.OpenRouter {
			req.Header.Set("X-Title", "unfraud")
		}
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
			lastErr = fmt.Errorf("%s: %s: %s", o.driver(), res.Status, truncate(raw))
			continue
		}
		var cr chatResponse
		if err := json.Unmarshal(raw, &cr); err != nil {
			return nil, fmt.Errorf("%s: %s: %s", o.driver(), res.Status, truncate(raw))
		}
		if res.StatusCode != http.StatusOK || cr.Error != nil {
			msg := string(truncate(raw))
			if cr.Error != nil {
				msg = cr.Error.Message
			}
			return nil, fmt.Errorf("%s: %s: %s", o.driver(), res.Status, msg)
		}
		return &cr, nil
	}
	return nil, lastErr
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func truncate(b []byte) []byte {
	if len(b) > 300 {
		return b[:300]
	}
	return b
}
