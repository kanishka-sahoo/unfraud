package features

import "context"

// QueryKind selects what a Query reads.
type QueryKind uint8

const (
	// QSum totals buckets From..To (inclusive) of a bucketed series.
	QSum QueryKind = iota + 1
	// QCountSince counts members of a set last seen at or after From (ms).
	QCountSince
	// QMember returns when Member was last seen in a set.
	QMember
	// QLast returns the time and value stored at a key.
	QLast
)

type Query struct {
	Kind     QueryKind
	Key      string
	From, To int64
	Member   string
}

type Result struct {
	Num  float64
	Str  string
	Time int64
	OK   bool
}

// UpdateKind selects what an Update writes.
type UpdateKind uint8

const (
	// UIncr adds Delta to bucket Bucket of a series, dropping buckets at or
	// below Bucket-Keep.
	UIncr UpdateKind = iota + 1
	// UTouch records that Member was seen in a set at Time.
	UTouch
	// USetLast stores Time and Str at a key.
	USetLast
)

type Update struct {
	Kind   UpdateKind
	Key    string
	Bucket int64
	Keep   int64
	Delta  float64
	Member string
	Time   int64
	Str    string
	// ExpireMs is the Event Time after which no query can see this state.
	ExpireMs int64
}

// Guard makes a batch of Updates idempotent: a State applies the Updates of
// a given Key at most once, until ExpireMs (Event Time). Kafka redelivers
// records after a crash or rebalance; without a Guard their feature updates
// would be counted twice.
type Guard struct {
	Key      string
	ExpireMs int64
}

// State stores feature state. Calls are batched per Event so that a remote
// implementation needs one round trip per Event. The
// Engine owns all feature logic; a State only stores and aggregates.
type State interface {
	// Exchange answers qs from the state as it was before applying us.
	// The Updates are skipped when g was already applied.
	Exchange(ctx context.Context, g Guard, qs []Query, out []Result, us []Update) error
	// Apply applies us unless g was already applied.
	Apply(ctx context.Context, g Guard, us []Update) error
	// Expire drops state whose ExpireMs is before nowMs.
	Expire(ctx context.Context, nowMs int64) error
}
