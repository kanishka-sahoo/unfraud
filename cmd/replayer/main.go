// Command replayer streams a Profile's dataset into its input topic in Event
// Time order, with delayed Label Releases and Watermarks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/replay"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("replay failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		profileName = flag.String("profile", "", "profile to replay: "+strings.Join(registry.Names(), ", "))
		dataDir     = flag.String("data", "data/raw", "root folder with one subfolder per profile")
		sinkName    = flag.String("sink", "kafka", "kafka, json (stdout) or discard")
		brokers     = flag.String("brokers", "localhost:9092", "comma-separated Kafka bootstrap servers")
		topic       = flag.String("topic", "", "input topic (default input.<profile>)")
		create      = flag.Int("create-partitions", 0, "create the topic with this many partitions if missing")
		speed       = flag.Float64("speed", 0, "Event Time per wall-clock time; 0 = as fast as possible")
		limit       = flag.Int("limit", 0, "stop after this many dataset records; 0 = all")
		labelDelay  = flag.Duration("label-delay", 0, "override the profile's label delay")
		cpuProfile  = flag.String("cpuprofile", "", "write a CPU profile to this file")
	)
	flag.Parse()

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}

	p, err := registry.Get(*profileName)
	if err != nil {
		return err
	}
	if *labelDelay > 0 {
		p.LabelDelay = *labelDelay
	}
	if *topic == "" {
		*topic = "input." + p.Name
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var sink replay.Sink
	switch *sinkName {
	case "kafka":
		ks, err := replay.NewKafkaSink(ctx, replay.KafkaConfig{
			Brokers: strings.Split(*brokers, ","), Topic: *topic, CreatePartitions: *create,
		})
		if err != nil {
			return err
		}
		defer ks.Close()
		log.Info("kafka sink ready", "topic", *topic, "partitions", ks.Partitions())
		sink = ks
	case "json":
		sink = replay.NewJSONSink(os.Stdout)
	case "discard":
		sink = replay.DiscardSink{}
	default:
		return fmt.Errorf("unknown sink %q", *sinkName)
	}

	dir := filepath.Join(*dataDir, p.Name)
	events, err := p.Open(dir)
	if err != nil {
		return err
	}
	defer events.Close()
	labels, err := p.Open(dir)
	if err != nil {
		return err
	}
	defer labels.Close()

	start := time.Now()
	log.Info("replay starting", "profile", p.Name, "speed", *speed, "label_delay", p.LabelDelay.String(),
		"watermark_interval", p.WatermarkInterval.String())
	stats, err := replay.Run(ctx, p.Name, events, labels, sink, replay.Options{
		LabelDelay:        p.LabelDelay,
		WatermarkInterval: p.WatermarkInterval,
		Speed:             *speed,
		Limit:             *limit,
		ProgressEvery:     10 * time.Second,
		Progress: func(s replay.Stats) {
			log.Info("progress", statsAttrs(s, start)...)
		},
	})
	if errors.Is(err, context.Canceled) {
		log.Warn("replay interrupted", statsAttrs(stats, start)...)
		return nil
	}
	if err != nil {
		return err
	}
	log.Info("replay finished", statsAttrs(stats, start)...)
	return nil
}

func statsAttrs(s replay.Stats, start time.Time) []any {
	elapsed := time.Since(start)
	return []any{
		"events", s.Events, "links", s.Links, "label_releases", s.LabelReleases, "watermarks", s.Watermarks,
		"event_time", time.UnixMilli(s.LastMs).UTC().Format(time.RFC3339),
		"elapsed", elapsed.Round(time.Millisecond).String(),
		"events_per_sec", int(float64(s.Events) / elapsed.Seconds()),
	}
}
