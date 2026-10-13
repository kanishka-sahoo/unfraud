// Package payments adapts payment datasets that have been normalized into the
// Payment Schema (train/src/unfraud_train/prepare.py). Every payment Profile
// shares the core columns, Entities, real-time features and rules, so a model
// trained on the core features of one Profile can score another's Events.
// Each Profile adds its own named extras.
package payments

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/csvx"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

// Entity types.
const (
	Payer    = "payer"    // the paying card or account
	Payee    = "payee"    // the merchant or receiving account
	Customer = "customer" // who owns the payer
)

// File is the normalized dataset in each Profile's raw folder.
const File = "events.csv"

// Core features, in schema order. hour and weekday come from Event Time.
var (
	CoreNum = []string{"amount_usd", "hour", "weekday"}
	CoreCat = []string{"channel", "category"}
)

// coreHeader is the fixed start of every events.csv.
var coreHeader = []string{"event_id", "event_time_ms", Payer, Payee, Customer, "amount_usd", "channel", "category", "is_fraud"}

// CoreSchema holds only the core features. Models trained on it bind to
// any payment Profile.
var CoreSchema = profile.NewSchema(CoreNum, CoreCat)

var coreGlossary = map[string]string{
	"amount_usd": "amount in US dollars",
	"hour":       "hour of day (dataset clock)",
	"weekday":    "day of week, 0 = Sunday",
	"channel":    "how the payment was made",
	"category":   "merchant category",
}

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

func w(windows ...time.Duration) []time.Duration { return windows }

// CoreFeatures are the real-time features every payment Profile computes.
var CoreFeatures = []profile.Feature{
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

// CoreRules were written from fraud-operations practice before any results.
// Each Profile reviews them on its validation period only and retires those
// less precise there than the model is at the review budget: a Rule only
// escalates, so a less precise one adds alerts the model would rank lower.
var CoreRules = []profile.Rule{
	{
		Code:        "KNOWN_FRAUD_PAYER",
		Description: "The payer has two or more confirmed fraud Labels in the last 30 days.",
		When:        []profile.Condition{{Feature: "payer_fraud_labels_30d", Op: profile.Ge, Value: 2}},
		Action:      profile.Block,
	},
	{
		Code:        "PRIOR_FRAUD_PAYER",
		Description: "The payer has a confirmed fraud Label in the last 90 days.",
		When:        []profile.Condition{{Feature: "payer_fraud_labels_90d", Op: profile.Ge, Value: 1}},
		Action:      profile.Review,
	},
	{
		Code:        "FRAUD_PAYEE",
		Description: "The payee received three or more payments confirmed as fraud in the last 30 days and is not paid by many.",
		When: []profile.Condition{
			{Feature: "payee_fraud_labels_30d", Op: profile.Ge, Value: 3},
			{Feature: "payee_payers_7d", Op: profile.Le, Value: 20},
		},
		Action: profile.Review,
	},
	{
		Code:        "PAYER_BURST",
		Description: "Ten or more payments by the payer within an hour.",
		When:        []profile.Condition{{Feature: "payer_count_1h", Op: profile.Ge, Value: 10}},
		Action:      profile.Review,
	},
	{
		Code:        "AMOUNT_SPIKE",
		Description: "At least $500 and ten times the payer's 30-day average.",
		When: []profile.Condition{
			{Feature: "amount_usd", Op: profile.Ge, Value: 500},
			{Feature: "payer_amount_ratio_30d", Op: profile.Ge, Value: 10},
		},
		Action: profile.Review,
	},
	{
		Code:        "NEW_PAYEE_HIGH_AMOUNT",
		Description: "At least $1,000 to a payee the payer never paid before.",
		When: []profile.Condition{
			{Feature: "amount_usd", Op: profile.Ge, Value: 1000},
			{Feature: "payer_new_payee", Op: profile.Eq, Value: 1},
		},
		Action: profile.Review,
	},
}

// Retire returns CoreRules with the given Rules retired, each with the
// validation-period evidence for it.
func Retire(evidence map[string]string) []profile.Rule {
	rules := slices.Clone(CoreRules)
	for i, r := range rules {
		if e, ok := evidence[r.Code]; ok {
			rules[i].Retired = e
			delete(evidence, r.Code)
		}
	}
	if len(evidence) > 0 {
		panic(fmt.Sprintf("payments: retiring unknown rules %v", evidence))
	}
	return rules
}

// Extra is a dataset-specific column of events.csv.
type Extra struct {
	Name, Description string
	Cat               bool
}

// New builds a payment Profile: the core schema followed by extras, the core
// features and glossary. Callers set rules, the graph, delays and splits.
func New(name string, extras []Extra) profile.Profile {
	num, cat := slices.Clone(CoreNum), slices.Clone(CoreCat)
	glossary := map[string]string{}
	for k, v := range coreGlossary {
		glossary[k] = v
	}
	header := slices.Clone(coreHeader)
	for _, e := range extras {
		if e.Cat {
			cat = append(cat, e.Name)
		} else {
			num = append(num, e.Name)
		}
		glossary[e.Name] = e.Description
		header = append(header, e.Name)
	}
	schema := profile.NewSchema(num, cat)
	return profile.Profile{
		Name:              name,
		WatermarkInterval: time.Hour,
		Schema:            schema,
		Features:          CoreFeatures,
		Glossary:          glossary,
		Open: func(dir string) (profile.Source, error) {
			return open(name, schema, header, filepath.Join(dir, File))
		},
	}
}

// slot is where an events.csv column goes in the schema.
type slot struct {
	num bool
	idx int
}

type source struct {
	name   string
	schema *profile.Schema
	r      *csvx.Reader
	slots  []slot // per column after the entity columns; idx -1 for is_fraud
}

func open(name string, schema *profile.Schema, header []string, path string) (profile.Source, error) {
	r, err := csvx.Open(path, true)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(r.Header, header) {
		r.Close()
		return nil, fmt.Errorf("%s: header %v, want %v (rerun make data)", path, r.Header, header)
	}
	s := &source{name: name, schema: schema, r: r}
	for _, col := range header[5:] {
		switch i, num := schema.NumIndex(col); {
		case col == "is_fraud":
			s.slots = append(s.slots, slot{idx: -1})
		case num:
			s.slots = append(s.slots, slot{num: true, idx: i})
		default:
			j, _ := schema.CatIndex(col)
			s.slots = append(s.slots, slot{idx: j})
		}
	}
	return s, nil
}

var (
	hourIdx, _    = CoreSchema.NumIndex("hour")
	weekdayIdx, _ = CoreSchema.NumIndex("weekday")
)

func (s *source) Next() (profile.Record, error) {
	row, err := s.r.Read()
	if err != nil {
		return profile.Record{}, err
	}
	t, err := strconv.ParseInt(row[1], 10, 64)
	if err != nil {
		return profile.Record{}, fmt.Errorf("%s line %d: event_time_ms: %w", s.name, s.r.Line(), err)
	}
	ev := &unfraudv1.Event{
		EventId:  strings.Clone(row[0]),
		Entities: make(map[string]string, 3),
		SchemaId: s.schema.ID,
		Num:      s.schema.NewNum(),
		Cat:      s.schema.NewCat(),
	}
	for i, typ := range []string{Payer, Payee, Customer} {
		if v := row[2+i]; v != "" {
			ev.Entities[typ] = strings.Clone(v)
		}
	}
	label := unfraudv1.Label_LABEL_LEGIT
	for i, v := range row[5:] {
		sl := s.slots[i]
		switch {
		case sl.idx < 0:
			if v == "1" {
				label = unfraudv1.Label_LABEL_FRAUD
			}
		case v == "":
		case sl.num:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return profile.Record{}, fmt.Errorf("%s line %d: %s: %w", s.name, s.r.Line(), s.schema.Num[sl.idx], err)
			}
			ev.Num[sl.idx] = f
		default:
			ev.Cat[sl.idx] = strings.Clone(v)
		}
	}
	tt := time.UnixMilli(t).UTC()
	ev.Num[hourIdx] = float64(tt.Hour())
	ev.Num[weekdayIdx] = float64(tt.Weekday())
	return profile.Record{Env: profile.NewEvent(s.name, t, ev), Key: ev.Entities[Payer], Label: label}, nil
}

func (s *source) Close() error { return s.r.Close() }
