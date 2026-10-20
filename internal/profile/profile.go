// Package profile defines how a dataset becomes an ordered stream of Events
// and Links. Adapters live in subpackages; services never import them
// directly, only through the registry.
package profile

import (
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
)

// Profile describes one dataset as the engine sees it.
type Profile struct {
	Name string
	// LabelDelay is how long after an Event's Event Time its Label is released.
	LabelDelay time.Duration
	// WatermarkInterval sets watermark granularity: a Watermark is emitted
	// whenever Event Time crosses a multiple of it.
	WatermarkInterval time.Duration
	// Schema is the feature layout of the Profile's Events.
	Schema *Schema
	// Features are the real-time features computed per Entity.
	Features []Feature
	// Rules are checked on every Event; they can only escalate a Decision.
	Rules []Rule
	// Graph, when set, adds attribute-graph features (internal/graph).
	Graph *GraphConfig
	// Glossary describes raw schema features whose meaning is published.
	// Raw features not in it are anonymized: Case Packets combine them
	// into one model signal instead of guessing what they mean.
	Glossary map[string]string
	// Nouns name Entity types the way analysts say them (payee: merchant),
	// in feature descriptions.
	Nouns map[string]string
	// Meanings spell out coded category values, per feature (an MCC's
	// merchant type).
	Meanings map[string]map[string]string
	// Amount is the raw feature holding the payment amount; a negative
	// amount is a refund (TabFormer has them), and Case Packets say so.
	Amount string
	// Headline lists the features that describe the payment itself, in the
	// order the console and Case Packets show them.
	Headline []string
	// Subject is the Entity type whose recent history an analyst reviews
	// with a Case (a card), and Behaviour the features that describe that
	// history, for Case Packets.
	Subject   string
	Behaviour []string
	// Split divides Event Time for training and evaluation.
	Split Split
	// Open starts reading the dataset from dataDir (the Profile's raw folder).
	Open func(dataDir string) (Source, error)
}

// Split divides a dataset by Event Time. Events before Train only warm up
// real-time features; [Train, Validation) trains, [Validation, Test)
// validates (thresholds, rule reviews) and everything from Test on is the
// test period.
type Split struct {
	Train, Validation, Test time.Time
}

// Record is one item read from a dataset.
type Record struct {
	// Env carries an Event or a Link, with Profile and Event Time set.
	Env *unfraudv1.Envelope
	// Key is the partition key: the Event's primary Entity id, or a Link's
	// source id.
	Key string
	// Label is the Event's Label, or LABEL_UNSPECIFIED when unknown or for Links.
	Label unfraudv1.Label
}

// Source yields Records in non-decreasing Event Time order.
type Source interface {
	// Next returns io.EOF after the last Record.
	Next() (Record, error)
	Close() error
}

// NewEvent builds an Event envelope.
func NewEvent(profile string, eventTimeMs int64, ev *unfraudv1.Event) *unfraudv1.Envelope {
	return &unfraudv1.Envelope{
		Profile:     profile,
		EventTimeMs: eventTimeMs,
		Payload:     &unfraudv1.Envelope_Event{Event: ev},
	}
}

// NewLink builds a Link envelope.
func NewLink(profile string, eventTimeMs int64, src, dst *unfraudv1.EntityRef) *unfraudv1.Envelope {
	return &unfraudv1.Envelope{
		Profile:     profile,
		EventTimeMs: eventTimeMs,
		Payload:     &unfraudv1.Envelope_Link{Link: &unfraudv1.Link{Src: src, Dst: dst}},
	}
}
