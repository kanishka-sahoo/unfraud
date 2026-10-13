package payments

import (
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// TabFormer is IBM's synthetic credit-card dataset: 2,000 US cardholders,
// several cards each. events.csv keeps October 2014 to December 2019.
var TabFormer = func() profile.Profile {
	p := New("tabformer", []Extra{
		{Name: "entry_mode", Description: "chip, swipe or online", Cat: true},
		{Name: "mcc", Description: "merchant category code", Cat: true},
		{Name: "errors", Description: "errors reported on the authorization, such as Bad PIN", Cat: true},
		{Name: "merchant_state", Description: "merchant's US state, or country when abroad", Cat: true},
		{Name: "abroad", Description: "1 if an in-person merchant is outside the US"},
		{Name: "out_of_home_state", Description: "1 if an in-person merchant is outside the cardholder's home state"},
		{Name: "card_brand", Description: "card network", Cat: true},
		{Name: "card_type", Description: "credit, debit or prepaid debit", Cat: true},
		{Name: "card_has_chip", Description: "1 if the card has a chip"},
		{Name: "credit_limit", Description: "card's credit limit in US dollars"},
		{Name: "customer_age", Description: "cardholder's age in years"},
		{Name: "yearly_income", Description: "cardholder's yearly income in US dollars"},
		{Name: "fico_score", Description: "cardholder's FICO credit score"},
	})
	p.LabelDelay = 30 * day
	p.Split = profile.Split{Train: date(2015, 1, 1), Validation: date(2018, 1, 1), Test: date(2019, 1, 1)}
	// The model's validation precision at the review budget is 7.1%.
	p.Rules = Retire(map[string]string{
		"KNOWN_FRAUD_PAYER":     "Validation: 11,680 hits, 0.31% precision.",
		"PRIOR_FRAUD_PAYER":     "Validation: 27,251 hits, 0.33% precision.",
		"PAYER_BURST":           "Validation: 7 hits, 0 fraud.",
		"AMOUNT_SPIKE":          "Validation: 3,959 hits, 0.99% precision.",
		"NEW_PAYEE_HIGH_AMOUNT": "Validation: 1,031 hits, 0.39% precision.",
	})
	// Chosen on the training years only: merchants used by 21-100 cards in
	// 30 days carry 3.2x the base fraud rate, those used by more 0.8x.
	p.Graph = &profile.GraphConfig{
		Node: Payer, Links: []string{Payee}, NodeNoun: "cards", LinkNoun: "merchants", MaxDegree: 100,
		Window: 30 * day, Rebuild: day, FraudRetention: 90 * day,
	}
	return p
}()

// Sparkov is the Kaggle "Credit Card Transactions Fraud Detection" dataset:
// 1,000 simulated cardholders with one card each and 693 merchants, 2019-2020.
var Sparkov = func() profile.Profile {
	p := New("sparkov", []Extra{
		{Name: "gender", Description: "cardholder's gender", Cat: true},
		{Name: "home_state", Description: "cardholder's US state", Cat: true},
		{Name: "city_pop", Description: "population of the cardholder's city"},
		{Name: "job", Description: "cardholder's occupation", Cat: true},
		{Name: "customer_age", Description: "cardholder's age in years"},
		{Name: "merchant_km", Description: "distance from the cardholder's home to the merchant in km"},
	})
	p.LabelDelay = 30 * day
	p.Split = profile.Split{Train: date(2019, 1, 1), Validation: date(2020, 4, 1), Test: date(2020, 7, 1)}
	// Every rule falls short of the model's 27.6% validation precision at
	// the review budget. Each card has one fraud episode, so earlier fraud
	// on a card never predicts more.
	p.Rules = Retire(map[string]string{
		"KNOWN_FRAUD_PAYER":     "Validation: 8,387 hits, 0 fraud.",
		"PRIOR_FRAUD_PAYER":     "Validation: 24,988 hits, 0 fraud.",
		"PAYER_BURST":           "Validation: no hits.",
		"FRAUD_PAYEE":           "Validation: 1,689 hits, 1.4% precision.",
		"AMOUNT_SPIKE":          "Validation: 1,427 hits, 18.4% precision.",
		"NEW_PAYEE_HIGH_AMOUNT": "Validation: 475 hits, 22.5% precision.",
	})
	// No graph: every merchant is shared by at least six cards a month and
	// most by hundreds, so all would be Super-nodes.
	return p
}()

// AMLWorld is IBM's synthetic anti-money-laundering dataset (HI-Small): bank
// transfers between 500,000 accounts over ten days, with laundering labelled.
// Labels stand in for fraud.
var AMLWorld = func() profile.Profile {
	p := New("amlworld", []Extra{
		{Name: "payment_currency", Description: "currency paid", Cat: true},
		{Name: "receiving_currency", Description: "currency received", Cat: true},
		{Name: "cross_currency", Description: "1 if paid and received currencies differ"},
		{Name: "self_transfer", Description: "1 if the account pays itself"},
		{Name: "same_bank", Description: "1 if both accounts are at the same bank"},
		{Name: "same_owner", Description: "1 if both accounts belong to the same entity"},
	})
	// Laundering is confirmed long after the fact in practice; the dataset
	// spans ten days, so a shorter delay keeps label features observable.
	p.LabelDelay = 3 * day
	p.Split = profile.Split{Train: date(2022, 9, 1), Validation: date(2022, 9, 7), Test: date(2022, 9, 9)}
	// The model's validation precision at the review budget is 4.1%. Hub
	// accounts make thousands of transfers, so payer rules fire on a tenth
	// of all traffic.
	p.Rules = Retire(map[string]string{
		"KNOWN_FRAUD_PAYER":     "Validation: 97,864 hits, 0.19% precision.",
		"PRIOR_FRAUD_PAYER":     "Validation: 102,198 hits, 0.20% precision.",
		"PAYER_BURST":           "Validation: 99,511 hits, 0.13% precision.",
		"AMOUNT_SPIKE":          "Validation: 6,645 hits, 0.84% precision.",
		"NEW_PAYEE_HIGH_AMOUNT": "Validation: 17,522 hits, 3.9% precision.",
	})
	// Chosen on the training days only: accounts paid by 6-20 others in a
	// week carry 7.2x the base laundering rate (fan-in), those paid by more
	// than 100 none.
	p.Graph = &profile.GraphConfig{
		Node: Payer, Links: []string{Payee}, NodeNoun: "accounts", LinkNoun: "receiving accounts", MaxDegree: 20,
		Window: 7 * day, Rebuild: day, FraudRetention: 30 * day,
	}
	return p
}()
