package tabformer

import "github.com/kanishka-sahoo/unfraud/internal/profile"

// rules were written from fraud-operations practice before any results. They
// are reviewed on the validation quarter only: a Rule less precise there than
// the model at the review budget (8.0%) is retired, since a Rule only
// escalates and a less precise one adds alerts the model would rank lower.
// FRAUD_PAYEE was 13.7% precise (1,602 hits).
var rules = []profile.Rule{
	{
		Code:        "FRAUD_PAYEE",
		Description: "The merchant received three or more payments confirmed as fraud in the last 30 days and is not used by many cards.",
		When: []profile.Condition{
			{Feature: "payee_fraud_labels_30d", Op: profile.Ge, Value: 3},
			{Feature: "payee_payers_7d", Op: profile.Le, Value: 20},
		},
		Action: profile.Review,
	},
	{
		Code:        "KNOWN_FRAUD_PAYER",
		Description: "The card has two or more confirmed fraud Labels in the last 30 days.",
		When:        []profile.Condition{{Feature: "payer_fraud_labels_30d", Op: profile.Ge, Value: 2}},
		Action:      profile.Block,
		Retired:     "Validation: 3,419 hits, 0.12% precision.",
	},
	{
		Code:        "PRIOR_FRAUD_PAYER",
		Description: "The card has a confirmed fraud Label in the last 90 days.",
		When:        []profile.Condition{{Feature: "payer_fraud_labels_90d", Op: profile.Ge, Value: 1}},
		Action:      profile.Review,
		Retired:     "Validation: 8,429 hits, 0.33% precision.",
	},
	{
		Code:        "PAYER_BURST",
		Description: "Ten or more payments on the card within an hour.",
		When:        []profile.Condition{{Feature: "payer_count_1h", Op: profile.Ge, Value: 10}},
		Action:      profile.Review,
		Retired:     "Validation: 1 hit, not fraud.",
	},
	{
		Code:        "AMOUNT_SPIKE",
		Description: "At least $500 and ten times the card's 30-day average.",
		When: []profile.Condition{
			{Feature: "amount_usd", Op: profile.Ge, Value: 500},
			{Feature: "payer_amount_ratio_30d", Op: profile.Ge, Value: 10},
		},
		Action:  profile.Review,
		Retired: "Validation: 931 hits, 1.4% precision.",
	},
	{
		Code:        "NEW_PAYEE_HIGH_AMOUNT",
		Description: "At least $1,000 at a merchant the card never used before.",
		When: []profile.Condition{
			{Feature: "amount_usd", Op: profile.Ge, Value: 1000},
			{Feature: "payer_new_payee", Op: profile.Eq, Value: 1},
		},
		Action:  profile.Review,
		Retired: "Validation: 242 hits, 0.83% precision.",
	},
}
