package profile

import "time"

// GraphConfig describes a Profile's attribute graph: Nodes (for example
// Card UIDs) become connected when they share a Link attribute (for example a
// Device Fingerprint). Attributes shared by more than MaxDegree Nodes within
// the Window are Super-nodes and never link.
type GraphConfig struct {
	Node      string
	Links     []string
	MaxDegree int
	Window    time.Duration
	// Rebuild is how often, in Event Time, the graph is rebuilt from the
	// Window's links; between rebuilds links are only added.
	Rebuild time.Duration
	// FraudRetention bounds how long a released fraud Label marks its Node.
	FraudRetention time.Duration
	// A Community becomes a Ring when it has at least RingMinNodes Nodes, of
	// which at least RingMinFraud, and at least RingMinShare of all, carry
	// released fraud Labels. RingMinNodes 0 disables Rings.
	RingMinNodes, RingMinFraud int
	RingMinShare               float64
}

// FlowConfig describes a dataset-supplied flow graph (Elliptic): Events are
// Nodes and Links are payments between them, complete only when a period
// closes. Aggregate lists numeric features averaged over each Node's
// neighbours.
type FlowConfig struct {
	Node      string
	Aggregate []string
}
