// Package tabformer adapts IBM's TabFormer synthetic credit-card dataset:
// 2,000 US cardholders with several cards each. unfraud-train prepare
// (train/src/unfraud_train/prepare.py) normalizes the raw files into
// events.csv, sorted by Event Time, October 2014 to December 2019.
package tabformer

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

const name = "tabformer"

// Entity types.
const (
	Payer    = "payer"    // a card: user and card index
	Payee    = "payee"    // a merchant
	Customer = "customer" // the cardholder
)

// File is the prepared dataset in the Profile's raw folder.
const File = "events.csv"

// header is events.csv's header: Event fields, then features. is_fraud is
// the Label; hour and weekday are derived from Event Time, not read.
var header = []string{
	"event_id", "event_time_ms", Payer, Payee, Customer, "amount_usd", "channel", "category", "is_fraud",
	"entry_mode", "mcc", "errors", "merchant_state", "abroad", "out_of_home_state", "card_brand", "card_type",
	"card_has_chip", "credit_limit", "customer_age", "yearly_income", "fico_score",
}

var schema = profile.NewSchema(
	[]string{"amount_usd", "hour", "weekday", "abroad", "out_of_home_state", "card_has_chip", "credit_limit",
		"customer_age", "yearly_income", "fico_score"},
	[]string{"channel", "category", "entry_mode", "mcc", "errors", "merchant_state", "card_brand", "card_type"},
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

var Profile = profile.Profile{
	Name:              name,
	LabelDelay:        30 * day,
	WatermarkInterval: time.Hour,
	Schema:            schema,
	Features:          features,
	Rules:             rules,
	Glossary: map[string]string{
		"amount_usd":        "amount in US dollars",
		"hour":              "hour of day (dataset clock)",
		"weekday":           "day of week, 0 = Sunday",
		"channel":           "in person or online",
		"category":          "merchant category (MCC grouped into 11)",
		"entry_mode":        "chip, swipe or online",
		"mcc":               "merchant category code",
		"errors":            "errors reported on the authorization, such as Bad PIN",
		"merchant_state":    "merchant's US state, or country when abroad",
		"abroad":            "1 if an in-person merchant is outside the US",
		"out_of_home_state": "1 if an in-person merchant is outside the cardholder's home state",
		"card_brand":        "card network",
		"card_type":         "credit, debit or prepaid debit",
		"card_has_chip":     "1 if the card has a chip",
		"credit_limit":      "card's credit limit in US dollars",
		"customer_age":      "cardholder's age in years",
		"yearly_income":     "cardholder's yearly income in US dollars",
		"fico_score":        "cardholder's FICO credit score",
	},
	Nouns: map[string]string{
		Payer: "card", Payee: "merchant", Customer: "cardholder",
		"amount_usd": "amount", "category": "merchant category", "channel": "channel",
	},
	Meanings: map[string]map[string]string{
		"mcc":     mccMeanings,
		"channel": {"in_person": "in person"},
	},
	Amount:   "amount_usd",
	Headline: []string{"amount_usd", "mcc", "channel", "entry_mode", "merchant_state", "errors", "card_brand", "card_type"},
	Subject:  Payer,
	Behaviour: []string{"payer_count_1h", "payer_count_24h", "payer_count_7d", "payer_amount_24h", "payer_amount_7d",
		"payer_amount_ratio_30d", "payer_payees_24h", "payer_new_payee", "payer_since_last", "payer_fraud_labels_30d",
		"graph_neighbor_fraud_nodes"},
	// Three months warm up the real-time features before training starts.
	// Training runs up to the last quarter before the test year, which
	// validates (early stopping, calibration, thresholds): fraud before 2018
	// looks different, and a model trained only on it misses most of 2019's
	// (ADR-0020).
	Split: profile.Split{Train: date(2015, 1, 1), Validation: date(2018, 10, 1), Test: date(2019, 1, 1)},
	// Chosen on the training years only: merchants used by 21-100 cards in
	// 30 days carry 3.2x the base fraud rate, those used by more 0.8x.
	Graph: &profile.GraphConfig{
		Node: Payer, Links: []string{Payee}, NodeNoun: "cards", LinkNoun: "merchants", MaxDegree: 100,
		Window: 30 * day, Rebuild: day, FraudRetention: 90 * day,
	},
	Open: func(dir string) (profile.Source, error) { return open(filepath.Join(dir, File)) },
}

// slot is where an events.csv column goes in the schema; idx -1 is the Label.
type slot struct {
	num bool
	idx int
}

type source struct {
	r     *csvx.Reader
	slots []slot // per column after the Entities
}

func open(path string) (profile.Source, error) {
	r, err := csvx.Open(path, true)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(r.Header, header) {
		r.Close()
		return nil, fmt.Errorf("%s: header %v, want %v (rerun make data)", path, r.Header, header)
	}
	s := &source{r: r}
	for _, col := range header[5:] {
		if col == "is_fraud" {
			s.slots = append(s.slots, slot{idx: -1})
		} else if i, ok := schema.NumIndex(col); ok {
			s.slots = append(s.slots, slot{num: true, idx: i})
		} else {
			i, _ := schema.CatIndex(col)
			s.slots = append(s.slots, slot{idx: i})
		}
	}
	return s, nil
}

var (
	hourIdx, _    = schema.NumIndex("hour")
	weekdayIdx, _ = schema.NumIndex("weekday")
)

func (s *source) Next() (profile.Record, error) {
	row, err := s.r.Read()
	if err != nil {
		return profile.Record{}, err
	}
	t, err := strconv.ParseInt(row[1], 10, 64)
	if err != nil {
		return profile.Record{}, fmt.Errorf("%s line %d: event_time_ms: %w", name, s.r.Line(), err)
	}
	ev := &unfraudv1.Event{
		EventId:  strings.Clone(row[0]),
		Entities: make(map[string]string, 3),
		SchemaId: schema.ID,
		Num:      schema.NewNum(),
		Cat:      schema.NewCat(),
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
				return profile.Record{}, fmt.Errorf("%s line %d: %s: %w", name, s.r.Line(), schema.Num[sl.idx], err)
			}
			ev.Num[sl.idx] = f
		default:
			ev.Cat[sl.idx] = strings.Clone(v)
		}
	}
	tt := time.UnixMilli(t).UTC()
	ev.Num[hourIdx] = float64(tt.Hour())
	ev.Num[weekdayIdx] = float64(tt.Weekday())
	return profile.Record{Env: profile.NewEvent(name, t, ev), Key: ev.Entities[Payer], Label: label}, nil
}

func (s *source) Close() error { return s.r.Close() }
