package cases

import (
	"slices"
	"testing"
)

var pk = Packet{
	CaseID: "ieee-cis/2987001", Dataset: "ieee-cis", EventTime: "2017-12-02T00:00:00Z",
	Decision: PacketDecision{Action: "block", Probability: 0.9873},
	Entities: map[string]string{"card_uid": "2755_nan_nan", "device": "SAMSUNG|Android 7.0"},
	Factors: []Factor{
		{Feature: "card_fraud_labels_30d", Value: 3.0, Effect: 2.413},
		{Feature: "TransactionAmt", Value: 1117.95, Effect: 0.512},
	},
}

func TestGroundAcceptsQuotedRoundedAndPercentNumbers(t *testing.T) {
	for _, text := range []string{
		"Blocked with probability 0.9873 on card 2755_nan_nan.",
		"A 98.7% fraud probability; the $1,117.95 payment raised risk.",
		"Two fraud labels in 30 days on device SAMSUNG|Android 7.0 (effect 2.4).",
		"Seen on 2017-12-02.",
	} {
		if p := Ground(text, pk); len(p) > 0 {
			t.Errorf("%q: unexpected problems %v", text, p)
		}
	}
}

func TestGroundRejectsInventedFacts(t *testing.T) {
	for text, want := range map[string][]string{
		"The $2,500 payment was blocked.":          {"number 2,500"},
		"Probability 0.95.":                        {"number 0.95"},
		"Card 1234_99_x was used.":                 {"identifier 1234_99_x"},
		"Linked to 47 other cards at 3 addresses.": {"number 47"},
	} {
		if got := Ground(text, pk); !slices.Equal(got, want) {
			t.Errorf("%q: problems %v, want %v", text, got, want)
		}
	}
}
