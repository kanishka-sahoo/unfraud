package tabformer

import (
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

func w(windows ...time.Duration) []time.Duration { return windows }

// features follow docs/design.md §5. Graph features come from the Profile's
// GraphConfig.
var features = []profile.Feature{
	// Velocity.
	{Name: "payer_count", Kind: profile.Count, Entity: Payer, Windows: w(hour, day, 7*day)},
	{Name: "payer_amount", Kind: profile.Sum, Entity: Payer, Field: "amount_usd", Windows: w(hour, day, 7*day)},
	{Name: "payer_payees", Kind: profile.Distinct, Entity: Payer, Of: Payee, Windows: w(day, 7*day)},
	{Name: "payee_count", Kind: profile.Count, Entity: Payee, Windows: w(hour, day)},
	{Name: "payee_payers", Kind: profile.Distinct, Entity: Payee, Of: Payer, Windows: w(day, 7*day)},
	{Name: "customer_payers", Kind: profile.Distinct, Entity: Customer, Of: Payer, Windows: w(7 * day)},

	// Unusual behaviour.
	{Name: "payer_amount_ratio", Kind: profile.RatioToMean, Entity: Payer, Field: "amount_usd", Windows: w(30 * day)},
	{Name: "payee_amount_ratio", Kind: profile.RatioToMean, Entity: Payee, Field: "amount_usd", Windows: w(7 * day)},
	{Name: "payer_since_last", Kind: profile.SinceLast, Entity: Payer},
	{Name: "payee_since_last", Kind: profile.SinceLast, Entity: Payee},
	{Name: "payer_new_payee", Kind: profile.NewPair, Entity: Payer, Of: Payee},
	{Name: "payer_channel_changed", Kind: profile.Changed, Entity: Payer, Field: "channel"},
	{Name: "payer_category_changed", Kind: profile.Changed, Entity: Payer, Field: "category"},

	// Released Labels (ADR-0004): visible only after the label delay.
	{Name: "payer_fraud_labels", Kind: profile.FraudLabels, Entity: Payer, Windows: w(30*day, 90*day)},
	{Name: "payee_fraud_labels", Kind: profile.FraudLabels, Entity: Payee, Windows: w(30 * day)},
	{Name: "customer_fraud_labels", Kind: profile.FraudLabels, Entity: Customer, Windows: w(90 * day)},
}
