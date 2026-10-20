package cases

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/profile/tabformer"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

func TestBuildDescribesThePaymentInPlainWords(t *testing.T) {
	c := store.Case{
		Profile: "tabformer", EventID: "10214163", EventTime: time.Date(2015, 5, 31, 11, 14, 0, 0, time.UTC),
		Entities: map[string]string{"payer": "856-1"}, Action: 2, Probability: 1, FlaggedBy: 1,
		Contributions: &store.CaseContributions{
			Top: []store.Contribution{{Feature: "mcc", Value: "5300", SHAP: 14.98}, {Feature: "payee_count_24h", Value: 69.0, SHAP: 2.89}},
			Values: map[string]any{"mcc": "5300", "payee_count_24h": 69.0, "amount_usd": 74.5, "channel": "online",
				"merchant_state": nil, "hour": 11.0},
		},
	}
	pk, err := Build(tabformer.Profile, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pk.CaseID != "10214163" {
		t.Errorf("case id %q should not carry the dataset", pk.CaseID)
	}
	if f := pk.Factors[0]; f.Meaning != "Wholesale clubs" {
		t.Errorf("mcc factor %+v has no meaning", f)
	}
	if f := pk.Factors[1]; f.Description != "payments by this merchant in the previous 24h" {
		t.Errorf("payee_count_24h described as %q", f.Description)
	}
	var got []string
	for _, f := range pk.Payment {
		got = append(got, f.Feature)
	}
	// Headline order; hour is a fact but not part of the payment headline.
	if want := []string{"amount_usd", "mcc", "channel", "merchant_state"}; !slices.Equal(got, want) {
		t.Errorf("payment facts %v, want %v", got, want)
	}
	all := Facts(tabformer.Profile, c.Contributions)
	if len(all) != 6 || all[len(all)-1].Feature != "payee_count_24h" {
		t.Errorf("facts %+v: headline first, then the rest by name", all)
	}
}

func TestBuildSummarizesTheCardsEarlierAlerts(t *testing.T) {
	at := time.Date(2015, 4, 3, 16, 18, 0, 0, time.UTC)
	c := store.Case{Profile: "tabformer", EventID: "1031376", EventTime: at, Entities: map[string]string{"payer": "77-1"},
		Action: 2, Probability: 1, Contributions: &store.CaseContributions{Values: map[string]any{"payer_amount_24h": 1631.26, "amount_usd": 59.85}}}
	earlier := []store.CaseListItem{
		{EventTime: at.Add(-7 * time.Hour), Action: 2, Probability: 0.964, Values: map[string]any{"amount_usd": 500.0, "mcc": "3504", "channel": "online"}},
		{EventTime: at.Add(-2 * time.Hour), Action: 1, Probability: 0.017, Values: map[string]any{"amount_usd": 53.46, "mcc": "8021"}},
	}
	pk, err := Build(tabformer.Profile, c, earlier)
	if err != nil {
		t.Fatal(err)
	}
	e := pk.Earlier
	if e == nil || e.Entity != "card 77-1" || e.Window != "3d" || e.Count != 2 || e.Blocked != 1 || e.TotalAmountUSD != 553.46 {
		t.Fatalf("earlier %+v", e)
	}
	if a := e.Alerts[0]; a.Merchant != "Hotel: Hilton" || a.Decision != "block" || a.EventTime != "2015-04-03 09:18" || a.Percent != "96.4%" || pk.Decision.Percent != ">99.9%" {
		t.Errorf("first earlier alert %+v", a)
	}
	if len(pk.Behaviour) != 1 || pk.Behaviour[0].Feature != "payer_amount_24h" {
		t.Errorf("behaviour %+v", pk.Behaviour)
	}
	// Quoting the precomputed totals passes Grounding.
	if p := Ground("The card had 2 earlier alerts worth $553.46, 1 blocked, including $500 at a hotel.", pk); len(p) > 0 {
		t.Errorf("grounding problems %v", p)
	}
}

// Alert 24076688: a brief saying the payment came "39 minutes" after the
// previous one was hidden, because the record only had 2340 seconds.
func TestBuildSpellsOutSecondsSoBriefsCanQuoteThem(t *testing.T) {
	c := store.Case{
		Profile: "tabformer", EventID: "24076688", EventTime: time.Date(2014, 10, 1, 18, 56, 0, 0, time.UTC),
		Entities: map[string]string{"payer": "1974-0"}, Action: 2, Probability: 0.9882, FlaggedBy: 1,
		Contributions: &store.CaseContributions{
			Top:    []store.Contribution{{Feature: "payer_since_last", Value: 2340.0, SHAP: 1.238}},
			Values: map[string]any{"payer_since_last": 2340.0, "amount_usd": 112.53},
		},
	}
	pk, err := Build(tabformer.Profile, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := pk.Factors[0]; f.Meaning != "39 minutes" {
		t.Errorf("payer_since_last factor %+v, want meaning 39 minutes", f)
	}
	if p := Ground("This payment came 39 minutes after the previous one.", pk); len(p) > 0 {
		t.Errorf("unexpected problems %v", p)
	}
}

// Alert 18255905: a -$361.00 refund was briefed as "a $361 online payment",
// because nothing in the record said a negative amount is a refund.
func TestBuildMarksNegativeAmountsAsRefunds(t *testing.T) {
	c := store.Case{
		Profile: "tabformer", EventID: "18255905", EventTime: time.Date(2015, 4, 11, 7, 41, 0, 0, time.UTC),
		Entities: map[string]string{"payer": "1474-3"}, Action: 2, Probability: 0.938, FlaggedBy: 2,
		Contributions: &store.CaseContributions{
			Top: []store.Contribution{{Feature: "payer_amount_ratio_30d", Value: -7.2287, SHAP: 1.1}},
			Values: map[string]any{"amount_usd": -361.0, "payer_amount_ratio_30d": -7.2287,
				"payer_since_last": 259200.0},
		},
	}
	pk, err := Build(tabformer.Profile, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := pk.Payment[0]; f.Feature != "amount_usd" || !strings.Contains(f.Meaning, "refund") {
		t.Errorf("amount fact %+v does not say it is a refund", f)
	}
	if f := pk.Factors[0]; !strings.Contains(f.Meaning, "refund") {
		t.Errorf("amount ratio factor %+v does not say it is a refund", f)
	}
}
