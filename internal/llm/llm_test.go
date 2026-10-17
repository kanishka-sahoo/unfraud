package llm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
)

var packet = cases.Packet{
	CaseID: "2987001", EventTime: "2017-12-02T00:00:00Z",
	Decision: cases.PacketDecision{Action: "block", Probability: 0.9873, FlaggedBy: "the graph revision"},
	Reasons:  []cases.PacketReason{{Code: "KNOWN_FRAUD_PAYER", Description: "two or more fraud labels"}},
	Entities: map[string]string{"payer": "2755_nan_nan", "payee": "SAMSUNG|Android 7.0"},
	Payment: []cases.Fact{
		{Feature: "amount_usd", Description: "amount", Value: 117.95},
		{Feature: "mcc", Description: "merchant category code", Value: "5300", Meaning: "Wholesale clubs"},
		{Feature: "channel", Description: "in person or online", Value: "online"},
	},
	Factors: []cases.Factor{
		{Feature: "mcc", Description: "merchant category code", Value: "5300", Meaning: "Wholesale clubs", Effect: 3.1},
		{Feature: "payer_fraud_labels_30d", Description: "confirmed fraud", Value: 3.0, Effect: 2.413},
		{Feature: "amount_usd", Description: "amount", Value: 117.95, Effect: 0.512},
		{Feature: "payer_since_last", Description: "seconds since", Value: 2762261.0, Effect: -0.3},
	},
	Anonymized: &cases.Factor{Feature: "anonymized", Effect: 0.88},
}

func TestStubIsGrounded(t *testing.T) {
	s, _, err := Stub{}.Summarize(context.Background(), packet)
	if err != nil {
		t.Fatal(err)
	}
	if p := cases.Ground(s.All(), packet); len(p) > 0 {
		t.Fatalf("stub summary not grounded: %v\n%s", p, s.All())
	}
	if !strings.HasPrefix(s.Text, "A payment of $117.95 at Wholesale clubs (merchant category 5300), online. Blocked") {
		t.Fatalf("stub does not describe the payment first:\n%s", s.Text)
	}
}

func TestAnthropicRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "k" {
			t.Errorf("path %s key %q", r.URL.Path, r.Header.Get("X-Api-Key"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		cfg := body["output_config"].(map[string]any)
		_, temp := body["temperature"]
		if body["model"] != "claude-haiku-5-5" || temp || cfg["effort"] != "low" || cfg["format"].(map[string]any)["type"] != "json_schema" {
			t.Errorf("request %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-haiku-5-5", "stop_reason": "end_turn",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "", "signature": "sig"},
				map[string]any{"type": "text", "text": `{"summary": "Blocked at 98.7% after a burst of payments on the card.", "key_points": ["Check: the card's other alerts", "Wholesale clubs"]}`},
			},
			"usage": map[string]any{"input_tokens": 1000, "output_tokens": 100},
		})
	}))
	defer srv.Close()
	s, meta, err := Anthropic{Key: "k", Model: "claude-haiku-5-5", Effort: "low", Options: []option.RequestOption{option.WithBaseURL(srv.URL)}}.
		Summarize(context.Background(), packet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "Blocked at 98.7% after a burst of payments on the card." || s.KeyPoints[1] != "Check: the card's other alerts" || meta.Model != "claude-haiku-5-5" || meta.PromptTokens != 1000 || math.Abs(meta.CostUSD-0.00015) > 1e-12 {
		t.Fatalf("summary %+v meta %+v", s, meta)
	}
}

func TestOpenRouterRequestAndRetry(t *testing.T) {
	zero := 0.0
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests) // first attempt is rate limited
			return
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["temperature"].(float64) != 0 || body["model"] != "m" {
			t.Errorf("request %v", body)
		}
		if p := body["provider"].(map[string]any); p["allow_fallbacks"] != false {
			t.Errorf("provider pin %v", p)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"model": "m-2026", "provider": "Up",
			"choices": []any{map[string]any{"message": map[string]any{"content": "```json\n{\"summary\": \"Blocked at 98.7% after a burst of payments on the card.\", \"key_points\": [\"Wholesale clubs\"]}\n```"}}},
			"usage":   map[string]any{"prompt_tokens": 900, "completion_tokens": 60, "cost": 0.0012},
		})
	}))
	defer srv.Close()
	s, meta, err := OpenAI{Key: "k", Model: "m", OpenRouter: true, Providers: []string{"Up"}, BaseURL: srv.URL, Temperature: &zero}.
		Summarize(context.Background(), packet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "Blocked at 98.7% after a burst of payments on the card." || meta.Model != "m-2026" || meta.Provider != "Up" || meta.CostUSD != 0.0012 || calls.Load() != 2 {
		t.Fatalf("summary %+v meta %+v calls %d", s, meta, calls.Load())
	}
}

func TestOpenAICompatibleSendsNoOpenRouterExtensions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		_, temp := body["temperature"]
		_, usage := body["usage"]
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Title") != "" || temp || usage {
			t.Errorf("path %s headers %v body %v", r.URL.Path, r.Header, body)
		}
		w.Write([]byte(`{"model":"llama","choices":[{"message":{"content":"{\"summary\": \"Blocked after a burst of payments on the card.\", \"key_points\": [\"A burst\"]}"}}]}`))
	}))
	defer srv.Close()
	s, meta, err := OpenAI{Model: "llama", BaseURL: srv.URL + "/v1/"}.Summarize(context.Background(), packet)
	if err != nil || s.Text != "Blocked after a burst of payments on the card." || meta.Driver != "openai" || meta.Provider != srv.URL+"/v1/" {
		t.Fatalf("summary %+v meta %+v err %v", s, meta, err)
	}
}

func TestOpenRouterRejectsNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"Sure! Here is a summary."}}]}`))
	}))
	defer srv.Close()
	_, _, err := OpenAI{Key: "k", Model: "m", BaseURL: srv.URL, OpenRouter: true}.Summarize(context.Background(), packet)
	if err == nil || !strings.Contains(err.Error(), "not the summary JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestStubCallsMissingValuesMissing(t *testing.T) {
	pk := packet
	pk.Factors = []cases.Factor{{Feature: "abroad", Description: "merchant abroad", Value: nil, Effect: 0.6}}
	s, _, err := Stub{}.Summarize(context.Background(), pk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.All(), "merchant abroad (missing)") {
		t.Fatalf("missing value rendered as <nil>:\n%s", s.All())
	}
}
