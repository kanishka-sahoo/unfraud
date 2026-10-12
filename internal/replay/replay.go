// Package replay turns a Profile's Source into the ordered input stream:
// Events and Links from the dataset, Label Releases delayed by the Profile's
// label delay, and Watermarks whenever Event Time crosses an interval.
package replay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

// Out is one record for a Sink.
type Out struct {
	Env *unfraudv1.Envelope
	// Key is the partition key. It is empty for Watermarks, which a Sink must
	// deliver to every partition.
	Key string
}

// Sink receives the input stream in order.
type Sink interface {
	Send(ctx context.Context, o Out) error
	// Flush blocks until everything sent has been delivered.
	Flush(ctx context.Context) error
}

type Options struct {
	LabelDelay        time.Duration
	WatermarkInterval time.Duration
	// Speed is Event Time per wall-clock time; 0 replays as fast as possible.
	Speed float64
	// Limit stops after this many dataset records; 0 means no limit.
	Limit int
	// Progress, if set, is called about every ProgressEvery with running stats.
	Progress      func(Stats)
	ProgressEvery time.Duration
	// Clock is replaceable for tests.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

type Stats struct {
	Events, Links, Watermarks, LabelReleases int
	// FirstMs and LastMs bound the Event Times sent.
	FirstMs, LastMs int64
}

// Run replays a dataset into sink. events and labels must be two independent
// Sources over the same dataset: labels trails events by the label delay and
// supplies the Label Releases, so memory stays constant however many Labels
// are pending (PaySim peaks at 2.9M with a 7-day delay).
func Run(ctx context.Context, profileName string, events, labels profile.Source, sink Sink, opt Options) (Stats, error) {
	if opt.WatermarkInterval <= 0 {
		return Stats{}, errors.New("replay: watermark interval must be positive")
	}
	if opt.LabelDelay <= 0 {
		return Stats{}, errors.New("replay: label delay must be positive")
	}
	if opt.now == nil {
		opt.now = time.Now
	}
	if opt.sleep == nil {
		opt.sleep = sleepCtx
	}
	r := &runner{
		profile: profileName, sink: sink, opt: opt,
		interval: opt.WatermarkInterval.Milliseconds(),
		labels:   &labelCursor{src: labels, delayMs: opt.LabelDelay.Milliseconds()},
	}
	if err := r.run(ctx, events); err != nil {
		return r.stats, err
	}
	return r.stats, sink.Flush(ctx)
}

type runner struct {
	profile  string
	sink     Sink
	opt      Options
	interval int64
	labels   *labelCursor
	// eventsRead counts Events taken from the events Source; the label cursor
	// never runs past it.
	eventsRead int
	stats      Stats
	started    bool
	draining   bool
	lastMs     int64
	// Pacing anchors: wall-clock start and the Event Time it corresponds to.
	wallStart    time.Time
	eventStartMs int64
	lastProgress time.Time
}

func (r *runner) run(ctx context.Context, src profile.Source) error {
	read := 0
	for r.opt.Limit == 0 || read < r.opt.Limit {
		rec, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		read++
		t := rec.Env.EventTimeMs
		if r.started && t < r.lastMs {
			return fmt.Errorf("replay: dataset out of order: record %d at %d after %d", read, t, r.lastMs)
		}
		// A Label released at time r is visible to Events at r or later, so
		// releases go first on ties.
		if err := r.releaseUntil(ctx, t); err != nil {
			return err
		}
		if err := r.emit(ctx, Out{Env: rec.Env, Key: rec.Key}); err != nil {
			return err
		}
		if rec.Env.GetEvent() != nil {
			r.eventsRead++
		}
	}
	// Labels for the last Events release after the dataset ends. Nothing
	// downstream waits on them, so they are not paced.
	r.draining = true
	if err := r.releaseUntil(ctx, math.MaxInt64); err != nil {
		return err
	}
	if r.started {
		return r.sendWatermark(ctx, r.lastMs)
	}
	return nil
}

// releaseUntil emits Label Releases due at or before t for Events already sent.
func (r *runner) releaseUntil(ctx context.Context, t int64) error {
	for {
		rel, ok, err := r.labels.peek(r.eventsRead)
		if err != nil || !ok || rel.releaseMs > t {
			return err
		}
		r.labels.pop()
		env := &unfraudv1.Envelope{
			Profile:     r.profile,
			EventTimeMs: rel.releaseMs,
			Payload: &unfraudv1.Envelope_LabelRelease{LabelRelease: &unfraudv1.LabelRelease{
				EventId: rel.eventID, Label: rel.label, LabelledEventTimeMs: rel.eventMs,
			}},
		}
		if err := r.emit(ctx, Out{Env: env, Key: rel.key}); err != nil {
			return err
		}
	}
}

// emit sends o, first closing any watermark bucket that o's Event Time leaves.
func (r *runner) emit(ctx context.Context, o Out) error {
	t := o.Env.EventTimeMs
	if !r.started {
		r.started = true
		r.stats.FirstMs = t
		r.wallStart = r.opt.now()
		r.eventStartMs = t
		r.lastProgress = r.wallStart
	} else if b := floorDiv(t, r.interval); b > floorDiv(r.lastMs, r.interval) {
		// Everything before the start of t's bucket is complete.
		if err := r.sendWatermark(ctx, b*r.interval-1); err != nil {
			return err
		}
	}
	if err := r.pace(ctx, t); err != nil {
		return err
	}
	if err := r.sink.Send(ctx, o); err != nil {
		return err
	}
	r.lastMs = t
	r.stats.LastMs = t
	switch o.Env.Payload.(type) {
	case *unfraudv1.Envelope_Event:
		r.stats.Events++
	case *unfraudv1.Envelope_Link:
		r.stats.Links++
	case *unfraudv1.Envelope_LabelRelease:
		r.stats.LabelReleases++
	}
	if r.opt.Progress != nil && r.opt.now().Sub(r.lastProgress) >= r.opt.ProgressEvery {
		r.lastProgress = r.opt.now()
		r.opt.Progress(r.stats)
	}
	return nil
}

func (r *runner) sendWatermark(ctx context.Context, ms int64) error {
	env := &unfraudv1.Envelope{
		Profile:     r.profile,
		EventTimeMs: ms,
		Payload:     &unfraudv1.Envelope_Watermark{Watermark: &unfraudv1.Watermark{}},
	}
	if err := r.sink.Send(ctx, Out{Env: env}); err != nil {
		return err
	}
	r.stats.Watermarks++
	return nil
}

// pace sleeps until the wall-clock moment that Event Time t maps to.
func (r *runner) pace(ctx context.Context, t int64) error {
	if r.opt.Speed <= 0 || r.draining {
		return nil
	}
	due := r.wallStart.Add(time.Duration(float64(t-r.eventStartMs) * float64(time.Millisecond) / r.opt.Speed))
	if d := due.Sub(r.opt.now()); d > 0 {
		return r.opt.sleep(ctx, d)
	}
	return nil
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type release struct {
	releaseMs, eventMs int64
	key, eventID       string
	label              unfraudv1.Label
}

// labelCursor walks a second Source over the dataset and yields the Label
// Release for each labelled Event, in Event order.
type labelCursor struct {
	src     profile.Source
	delayMs int64
	events  int // Events consumed from src, labelled or not
	next    *release
	nextIdx int // index of next among Events
	done    bool
}

// peek returns the next release whose Event index is below limit.
func (c *labelCursor) peek(limit int) (release, bool, error) {
	for c.next == nil && !c.done {
		if c.events >= limit {
			return release{}, false, nil
		}
		rec, err := c.src.Next()
		if err == io.EOF {
			c.done = true
			break
		}
		if err != nil {
			return release{}, false, fmt.Errorf("label cursor: %w", err)
		}
		ev := rec.Env.GetEvent()
		if ev == nil {
			continue
		}
		idx := c.events
		c.events++
		if rec.Label == unfraudv1.Label_LABEL_UNSPECIFIED {
			continue
		}
		t := rec.Env.EventTimeMs
		c.next = &release{
			releaseMs: t + c.delayMs, eventMs: t,
			// Clone so the release doesn't pin the CSV line it was sliced from.
			key: strings.Clone(rec.Key), eventID: strings.Clone(ev.EventId), label: rec.Label,
		}
		c.nextIdx = idx
	}
	if c.next == nil || c.nextIdx >= limit {
		return release{}, false, nil
	}
	return *c.next, true, nil
}

func (c *labelCursor) pop() { c.next = nil }
