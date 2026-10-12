package ieeecis

import "github.com/kanishka-sahoo/unfraud/internal/profile"

// rules were written from fraud-operations practice before any results. They
// are reviewed on the validation month only (never the test month): a Rule
// that catches no fraud there is retired.
var rules = []profile.Rule{
	{
		Code:        "KNOWN_FRAUD_CARD",
		Description: "The card has two or more confirmed fraud Labels in the last 30 days.",
		When:        []profile.Condition{{Feature: "card_fraud_labels_30d", Op: profile.Ge, Value: 2}},
		Action:      profile.Block,
	},
	{
		Code:        "PRIOR_FRAUD_CARD",
		Description: "The card has a confirmed fraud Label in the last 90 days.",
		When:        []profile.Condition{{Feature: "card_fraud_labels_90d", Op: profile.Ge, Value: 1}},
		Action:      profile.Review,
	},
	{
		Code:        "FRAUD_DEVICE",
		Description: "The device has confirmed fraud in the last 30 days and is not a widely shared fingerprint.",
		When: []profile.Condition{
			{Feature: "device_fraud_labels_30d", Op: profile.Ge, Value: 2},
			{Feature: "device_cards_7d", Op: profile.Le, Value: 10},
		},
		Action: profile.Review,
	},
	{
		Code:        "DEVICE_CARD_BURST",
		Description: "Five or more cards used on one device within an hour.",
		When:        []profile.Condition{{Feature: "device_cards_1h", Op: profile.Ge, Value: 5}},
		Action:      profile.Review,
	},
	{
		Code:        "CARD_VELOCITY",
		Description: "Ten or more transactions on the card within an hour.",
		When:        []profile.Condition{{Feature: "card_count_1h", Op: profile.Ge, Value: 10}},
		Action:      profile.Review,
		Retired:     "Validation month: 3 hits, 0 fraud. (Test month: 1,245 hits, 0 fraud.)",
	},
	{
		Code:        "AMOUNT_SPIKE",
		Description: "At least $500 and ten times the card's 7-day average.",
		When: []profile.Condition{
			{Feature: "TransactionAmt", Op: profile.Ge, Value: 500},
			{Feature: "card_amount_ratio_7d", Op: profile.Ge, Value: 10},
		},
		Action:  profile.Review,
		Retired: "Validation month: 143 hits, 0 fraud.",
	},
	{
		Code:        "NEW_DEVICE_HIGH_AMOUNT",
		Description: "At least $1,000 from a device never seen with this card.",
		When: []profile.Condition{
			{Feature: "TransactionAmt", Op: profile.Ge, Value: 1000},
			{Feature: "card_new_device", Op: profile.Eq, Value: 1},
		},
		Action:  profile.Review,
		Retired: "Validation month: 5 hits, 0 fraud.",
	},
}
