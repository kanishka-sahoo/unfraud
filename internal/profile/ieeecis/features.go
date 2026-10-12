package ieeecis

import (
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

func w(windows ...time.Duration) []time.Duration { return windows }

// features follow docs/design.md §5. Graph features arrive with graph-svc.
var features = []profile.Feature{
	// Velocity.
	{Name: "card_count", Kind: profile.Count, Entity: CardUID, Windows: w(hour, day, 7*day)},
	{Name: "card_amount", Kind: profile.Sum, Entity: CardUID, Field: "TransactionAmt", Windows: w(hour, day, 7*day)},
	{Name: "device_count", Kind: profile.Count, Entity: Device, Windows: w(hour, day)},
	{Name: "device_cards", Kind: profile.Distinct, Entity: Device, Of: CardUID, Windows: w(hour, day, 7*day)},
	{Name: "card_devices", Kind: profile.Distinct, Entity: CardUID, Of: Device, Windows: w(day, 7*day)},
	{Name: "card_emails", Kind: profile.Distinct, Entity: CardUID, Of: EmailP, Windows: w(day, 7*day)},
	{Name: "addr_cards", Kind: profile.Distinct, Entity: Addr, Of: CardUID, Windows: w(hour, day)},

	// Unusual behaviour.
	{Name: "card_amount_ratio", Kind: profile.RatioToMean, Entity: CardUID, Field: "TransactionAmt", Windows: w(7 * day)},
	{Name: "card_since_last", Kind: profile.SinceLast, Entity: CardUID},
	{Name: "device_since_last", Kind: profile.SinceLast, Entity: Device},
	{Name: "card_new_device", Kind: profile.NewPair, Entity: CardUID, Of: Device},
	{Name: "card_new_email", Kind: profile.NewPair, Entity: CardUID, Of: EmailP},

	// Movement. The dataset has no coordinates, only address codes and dist1.
	{Name: "card_addr_changed", Kind: profile.Changed, Entity: CardUID, Field: "addr1"},
	{Name: "card_dist1_delta", Kind: profile.Delta, Entity: CardUID, Field: "dist1"},

	// Released Labels (ADR-0004): visible only after the label delay.
	{Name: "card_fraud_labels", Kind: profile.FraudLabels, Entity: CardUID, Windows: w(30*day, 90*day)},
	{Name: "device_fraud_labels", Kind: profile.FraudLabels, Entity: Device, Windows: w(30*day, 90*day)},
	{Name: "email_fraud_labels", Kind: profile.FraudLabels, Entity: EmailP, Windows: w(30 * day)},
	{Name: "addr_fraud_labels", Kind: profile.FraudLabels, Entity: Addr, Windows: w(30 * day)},
}
