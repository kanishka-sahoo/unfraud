package features_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/profile/payments"
	"github.com/kanishka-sahoo/unfraud/internal/replay"
)

// TestValkeyMatchesMemory replays TabFormer through two engines, one on each
// State, delivering every record twice, and requires bit-identical features. It needs a Valkey server
// (UNFRAUD_VALKEY_ADDR=localhost:6390) and the dataset in data/raw.
func TestValkeyMatchesMemory(t *testing.T) {
	addr := os.Getenv("UNFRAUD_VALKEY_ADDR")
	if addr == "" {
		t.Skip("UNFRAUD_VALKEY_ADDR not set")
	}
	dir := "../../data/raw/tabformer"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("TabFormer data not prepared")
	}
	c, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	prefix := fmt.Sprintf("test-%d:", time.Now().UnixNano())

	p := payments.TabFormer
	mem, err := features.New(p, features.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	val, err := features.New(p, features.NewValkey(c, prefix))
	if err != nil {
		t.Fatal(err)
	}
	events, _ := p.Open(dir)
	defer events.Close()
	labels, _ := p.Open(dir)
	defer labels.Close()

	sink := &compareSink{t: t, a: mem, b: val, cols: mem.Columns()}
	limit := 50_000
	if testing.Short() {
		limit = 5_000
	}
	// A short label delay makes Label Releases and expiry happen within the sample.
	_, err = replay.Run(context.Background(), p.Name, events, labels, sink, replay.Options{
		LabelDelay: 6 * time.Hour, WatermarkInterval: time.Hour, Limit: limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("compared %d Events x %d features, %d non-missing values", sink.events, len(sink.cols), sink.values)
}

type compareSink struct {
	t              *testing.T
	a, b           *features.Engine
	cols           []string
	events, values int
}

func (s *compareSink) Send(ctx context.Context, o replay.Out) error {
	va, err := s.a.Process(ctx, o.Env)
	if err != nil {
		return err
	}
	vb, err := s.b.Process(ctx, o.Env)
	if err != nil {
		return err
	}
	// Redeliver every record, as Kafka does after a crash: the Guard must
	// keep both States from applying it twice.
	if _, err := s.a.Process(ctx, o.Env); err != nil {
		return err
	}
	if _, err := s.b.Process(ctx, o.Env); err != nil {
		return err
	}
	if va == nil {
		return nil
	}
	s.events++
	for i := range va {
		same := math.Float64bits(va[i]) == math.Float64bits(vb[i]) || math.IsNaN(va[i]) && math.IsNaN(vb[i])
		if !same {
			return fmt.Errorf("event %s (#%d) %s: memory %v, valkey %v",
				o.Env.GetEvent().EventId, s.events, s.cols[i], va[i], vb[i])
		}
		if !math.IsNaN(va[i]) {
			s.values++
		}
	}
	return nil
}

func (s *compareSink) Flush(context.Context) error { return nil }
