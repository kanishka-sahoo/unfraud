package cases

// History is an Entity's alerts over a longer window than Earlier: how often
// the card was flagged, and how analysts resolved those alerts. Totals are
// precomputed, as Grounding only accepts numbers found in the Packet.
type History struct {
	Entity         string      `json:"entity"` // "card 77-1"
	Window         string      `json:"window"` // "30 days"
	Alerts         int         `json:"alerts"`
	Blocked        int         `json:"blocked"`
	Confirmed      int         `json:"confirmed_fraud"`
	Dismissed      int         `json:"dismissed"`
	Escalated      int         `json:"escalated"`
	Open           int         `json:"not_yet_reviewed"`
	TotalAmountUSD float64     `json:"total_amount_usd"`
	FirstAlert     string      `json:"first_alert,omitempty"`
	LastAlert      string      `json:"last_alert,omitempty"`
	ByMerchantType []TypeCount `json:"by_merchant_type,omitempty"` // most alerts first, at most 5
	// Shown says when only the newest alerts were counted.
	Shown string `json:"counted,omitempty"`
}

// TypeCount is how many alerts, and how much money, went to one merchant type.
type TypeCount struct {
	MerchantType string  `json:"merchant_type"`
	MCC          any     `json:"mcc,omitempty"`
	Alerts       int     `json:"alerts"`
	AmountUSD    float64 `json:"amount_usd"`
}

// MerchantActivity is a merchant's alerts across all cards in a window.
type MerchantActivity struct {
	Entity       string `json:"entity"` // "merchant 5432"
	MerchantType string `json:"merchant_type,omitempty"`
	Window       string `json:"window"`
	Alerts       int    `json:"alerts"`
	Cards        int    `json:"cards"`
	Blocked      int    `json:"blocked"`
	Confirmed    int    `json:"confirmed_fraud"`
	Dismissed    int    `json:"dismissed"`
	Shown        string `json:"counted,omitempty"`
}

// Network is the neighbourhood of a card in graph-svc's current graph: the
// merchants it used in the graph's window and the other cards that used them.
type Network struct {
	Entity string `json:"entity"` // "card 77-1"
	// AsOf is the dataset time graph-svc has reached, which can be long
	// after the payment being investigated.
	AsOf       string `json:"graph_as_of,omitempty"`
	Hops       int    `json:"hops"`
	Merchants  int    `json:"merchants"`
	OtherCards int    `json:"other_cards"`
	// FraudCards counts other cards in the neighbourhood with confirmed
	// (released) fraud; FraudLinks says how up to 10 of them connect.
	FraudCards int         `json:"cards_with_confirmed_fraud"`
	FraudLinks []FraudLink `json:"cards_with_confirmed_fraud_links,omitempty"`
	// TopMerchants are the card's merchants that link it to the most
	// other cards, at most 5.
	TopMerchants []MerchantLink `json:"merchants_linking_most_cards,omitempty"`
	// BusyMerchants are merchants used by so many cards that the graph
	// does not link through them.
	BusyMerchants int    `json:"busy_merchants_not_linked_through,omitempty"`
	CardHasFraud  bool   `json:"card_has_confirmed_fraud"`
	Truncated     bool   `json:"truncated,omitempty"`
	Note          string `json:"note"`
}

// FraudLink is a card with confirmed fraud near the investigated card and
// the investigated card's merchants it also used.
type FraudLink struct {
	Card            string   `json:"card"`
	SharedMerchants []string `json:"shared_merchants,omitempty"`
	// Via is set when the card shares no merchant directly.
	Via string `json:"linked_via,omitempty"`
}

type MerchantLink struct {
	Merchant string `json:"merchant"`
	Cards    int    `json:"other_cards"`
}

// Related lists other cards' alerts at the same merchant near this one.
type Related struct {
	Merchant string         `json:"merchant"`
	Window   string         `json:"window"` // "24 hours either side"
	Count    int            `json:"count"`
	Cards    int            `json:"cards"`
	Alerts   []RelatedAlert `json:"alerts"` // at most 8, nearest first
}

type RelatedAlert struct {
	AlertID     string `json:"alert_id"`
	Card        string `json:"card"`
	EventTime   string `json:"event_time"`
	Decision    string `json:"decision"`
	AmountUSD   any    `json:"amount_usd"`
	Percent     string `json:"fraud_probability_percent"`
	Disposition string `json:"analyst_outcome,omitempty"`
}

// Stage is one Stage's Decision on the payment.
type Stage struct {
	Stage    string `json:"stage"` // "real-time scorer", "graph revision"
	Decision string `json:"decision"`
	Percent  string `json:"fraud_probability_percent"`
	Model    string `json:"model"`
}
