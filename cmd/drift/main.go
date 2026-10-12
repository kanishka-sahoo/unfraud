// Command drift compares a System Run's Decisions (the scored.<profile>
// topic) with an Accuracy Run's (decisions.parquet). Differences come from
// running partitions in parallel against shared state: an Event may see a
// slightly different history live than in Event Time order (ADR-0006).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/table"
)

type accuracy struct {
	prob   float32
	action int64
}

type Report struct {
	Topic           string  `json:"topic"`
	ElapsedSeconds  int     `json:"elapsed_seconds,omitempty"`
	EventsPerSecond int     `json:"events_per_second,omitempty"`
	Compared        int     `json:"compared"`
	MissingFromRun  int     `json:"missing_from_accuracy_run"`
	Duplicates      int     `json:"duplicates"`
	ProbDiffering   int     `json:"probability_differs"`
	MaxAbsProbDiff  float64 `json:"max_abs_probability_diff"`
	MeanAbsProbDiff float64 `json:"mean_abs_probability_diff"`
	P99AbsProbDiff  float64 `json:"p99_abs_probability_diff"`
	ActionDiffering int     `json:"action_differs"`
	LatencyP50Ms    int64   `json:"end_to_end_latency_p50_ms"`
	LatencyP99Ms    int64   `json:"end_to_end_latency_p99_ms"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "drift:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		profile   = flag.String("profile", "", "profile")
		brokers   = flag.String("brokers", "localhost:30092", "Kafka bootstrap servers")
		decisions = flag.String("decisions", "", "Accuracy Run decisions.parquet")
		countOnly = flag.Bool("count-only", false, "print how many Decisions the Accuracy Run has and exit")
		elapsed   = flag.Int("elapsed", 0, "System Run duration in seconds, to report throughput")
		topicFlag = flag.String("topic", "", "topic to compare (default scored.<profile>)")
	)
	flag.Parse()
	want, err := readDecisions(*decisions)
	if err != nil {
		return err
	}
	if *countOnly {
		fmt.Println(len(want))
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	topic := "scored." + *profile
	if *topicFlag != "" {
		topic = *topicFlag
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(*brokers, ",")...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		return err
	}
	defer cl.Close()
	ends, err := kadm.NewClient(cl).ListEndOffsets(ctx, topic)
	if err != nil {
		return err
	}
	remaining := map[int32]int64{}
	ends.Each(func(o kadm.ListedOffset) {
		if o.Offset > 0 {
			remaining[o.Partition] = o.Offset
		}
	})

	rep := Report{Topic: topic, ElapsedSeconds: *elapsed}
	if *elapsed > 0 {
		rep.EventsPerSecond = len(want) / *elapsed
	}
	seen := map[string]bool{}
	var diffs []float64
	var lat []int64
	for len(remaining) > 0 {
		fs := cl.PollFetches(ctx)
		if err := fs.Err0(); err != nil {
			return err
		}
		fs.EachRecord(func(r *kgo.Record) {
			if end, ok := remaining[r.Partition]; ok && r.Offset+1 >= end {
				delete(remaining, r.Partition)
			}
			var s unfraudv1.Scored
			if proto.Unmarshal(r.Value, &s) != nil {
				return
			}
			if seen[s.EventId] {
				rep.Duplicates++
				return
			}
			seen[s.EventId] = true
			a, ok := want[s.EventId]
			if !ok {
				rep.MissingFromRun++
				return
			}
			rep.Compared++
			d := math.Abs(float64(float32(s.Probability)) - float64(a.prob))
			diffs = append(diffs, d)
			if d > 0 {
				rep.ProbDiffering++
			}
			if int64(s.ModelAction)-1 != a.action {
				rep.ActionDiffering++
			}
			lat = append(lat, s.ScoredAtMs-s.IngestedAtMs)
		})
	}
	if len(diffs) > 0 {
		slices.Sort(diffs)
		slices.Sort(lat)
		var sum float64
		for _, d := range diffs {
			sum += d
		}
		rep.MaxAbsProbDiff = diffs[len(diffs)-1]
		rep.MeanAbsProbDiff = sum / float64(len(diffs))
		rep.P99AbsProbDiff = diffs[len(diffs)*99/100]
		rep.LatencyP50Ms = lat[len(lat)/2]
		rep.LatencyP99Ms = lat[len(lat)*99/100]
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func readDecisions(path string) (map[string]accuracy, error) {
	r, err := table.Open(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	id, _ := r.Index("event_id")
	pr, _ := r.Index("probability")
	act, _ := r.Index("model_action")
	out := map[string]accuracy{}
	for {
		row, err := r.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out[string(row[id].ByteArray())] = accuracy{prob: row[pr].Float(), action: row[act].Int64()}
	}
}
