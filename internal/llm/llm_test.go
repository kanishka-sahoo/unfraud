package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
)

var packet = cases.Packet{
	CaseID: "tabformer/2987001", Dataset: "tabformer", EventTime: "2017-12-02T00:00:00Z",
	Decision: cases.PacketDecision{Action: "block", Probability: 0.9873, FlaggedBy: "the graph revision"},
	Reasons:  []cases.PacketReason{{Code: "KNOWN_FRAUD_PAYER", Description: "two or more fraud labels"}},
	Entities: map[string]string{"payer": "2755_nan_nan", "payee": "SAMSUNG|Android 7.0"},
	Factors: []cases.Factor{
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
}

func TestOpenRouterRequestAndRetry(t *testing.T) {
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
			"choices": []any{map[string]any{"message": map[string]any{"content": "```json\n{\"summary\": \"Blocked at 98.7%.\", \"key_points\": []}\n```"}}},
			"usage":   map[string]any{"prompt_tokens": 900, "completion_tokens": 60, "cost": 0.0012},
		})
	}))
	defer srv.Close()
	s, meta, err := OpenRouter{Key: "k", Model: "m", Providers: []string{"Up"}, BaseURL: srv.URL}.Summarize(context.Background(), packet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "Blocked at 98.7%." || meta.Model != "m-2026" || meta.Provider != "Up" || meta.CostUSD != 0.0012 || calls.Load() != 2 {
		t.Fatalf("summary %+v meta %+v calls %d", s, meta, calls.Load())
	}
}

func TestOpenRouterRejectsNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"Sure! Here is a summary."}}]}`))
	}))
	defer srv.Close()
	_, _, err := OpenRouter{Key: "k", Model: "m", BaseURL: srv.URL}.Summarize(context.Background(), packet)
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
	if !strings.Contains(s.All(), "abroad (missing)") {
		t.Fatalf("missing value rendered as <nil>:\n%s", s.All())
	}
}
