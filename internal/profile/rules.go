package profile

// Action is what a Decision does with an Event. Higher values are stricter.
type Action int

const (
	Approve Action = iota
	Review
	Block
)

func (a Action) String() string {
	return [...]string{"approve", "review", "block"}[a]
}

// Rule is a fast, explainable check. All Conditions must hold for it to fire.
type Rule struct {
	// Code is the stable Reason Code, e.g. "DEVICE_CARD_BURST".
	Code        string
	Description string
	When        []Condition
	Action      Action
	// Retired, when set, records why the Rule no longer runs. Retired Rules
	// stay declared so the decision and its evidence remain visible.
	Retired string
}

// Condition compares one feature with a constant. Feature names a raw schema
// feature or a real-time feature. Missing values never satisfy a Condition.
type Condition struct {
	Feature string
	Op      Op
	Value   float64 // numeric features
	Str     string  // categorical features (Eq, Ne only)
}

type Op string

const (
	Gt Op = ">"
	Ge Op = ">="
	Lt Op = "<"
	Le Op = "<="
	Eq Op = "=="
	Ne Op = "!="
)
