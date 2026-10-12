package store

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// testStore needs a disposable Postgres:
// UNFRAUD_PG_URL=postgres://postgres:test@localhost:5433/postgres
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("UNFRAUD_PG_URL")
	if url == "" {
		t.Skip("UNFRAUD_PG_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	db := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgx.ParseConfig(url)
	s, err := Open(ctx, fmt.Sprintf("postgres://%s:%s@%s:%d/%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, db))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		admin.Exec(ctx, "DROP DATABASE "+db)
		admin.Close(ctx)
	})
	return s
}

func dec(id string, stage, action int16, prob float32, codes ...string) Decision {
	t := time.Unix(1_500_000_000, 0)
	return Decision{Profile: "p", EventID: id, Stage: stage, EventTime: t, Probability: prob, ModelAction: action,
		Action: action, ReasonCodes: codes, ModelVersion: fmt.Sprintf("m%d", stage), ScoredAt: t, IngestedAt: t,
		Entities: map[string]string{"card": "c-" + id}}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	s := testStore(t)
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
}

func TestDecisionsIgnoreRedelivery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ds := []Decision{dec("a", 1, 0, .1), dec("a", 2, 1, .6), dec("b", 1, 0, .2)}
	for range 2 {
		if err := s.InsertDecisions(ctx, ds); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	s.Pool.QueryRow(ctx, `SELECT count(*) FROM decisions`).Scan(&n)
	if n != 3 {
		t.Fatalf("%d decisions stored, want 3", n)
	}
}

func TestCasesOnlyEverGetStricter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	need, err := s.UpsertCases(ctx, []Decision{dec("a", 1, 0, .1), dec("b", 1, 1, .5, "RULE_X")})
	if err != nil || len(need) != 1 || need[0].EventID != "b" {
		t.Fatalf("first batch: need %v, err %v; want a new Case for b only", need, err)
	}
	s.SetContributions(ctx, need[0], CaseContributions{Stage: 1})

	// A redelivery and a same-action Revision change nothing that needs SHAP.
	need, _ = s.UpsertCases(ctx, []Decision{dec("b", 1, 1, .5, "RULE_X"), dec("b", 2, 1, .55, "RULE_Y")})
	if len(need) != 0 {
		t.Fatalf("same action asked for new Contributions: %v", need)
	}
	// A stricter Revision raises the action and asks for Contributions again.
	need, _ = s.UpsertCases(ctx, []Decision{dec("b", 2, 2, .99)})
	if len(need) != 1 {
		t.Fatalf("stricter action did not ask for Contributions")
	}
	// A later, weaker Decision never lowers it.
	s.UpsertCases(ctx, []Decision{dec("b", 1, 1, .4)})

	var action int16
	var prob float32
	var codes []string
	var version string
	s.Pool.QueryRow(ctx, `SELECT action, probability, reason_codes, model_version FROM cases WHERE event_id = 'b'`).
		Scan(&action, &prob, &codes, &version)
	if action != 2 || prob != .99 || version != "m2" || !slices.Equal(codes, []string{"RULE_X", "RULE_Y"}) {
		t.Fatalf("case b: action %d prob %v version %s codes %v", action, prob, version, codes)
	}
}

func TestOneBatchMayFlagACaseTwice(t *testing.T) {
	s := testStore(t)
	need, err := s.UpsertCases(context.Background(), []Decision{dec("c", 1, 1, .5, "A"), dec("c", 2, 2, .9, "B"), dec("c", 1, 1, .5, "C")})
	if err != nil || len(need) != 1 {
		t.Fatalf("need %v, err %v", need, err)
	}
	var codes []string
	var action int16
	s.Pool.QueryRow(context.Background(), `SELECT action, reason_codes FROM cases WHERE event_id = 'c'`).Scan(&action, &codes)
	if action != 2 || !slices.Equal(codes, []string{"A", "B", "C"}) {
		t.Fatalf("action %d codes %v", action, codes)
	}
}

func TestEqualActionsPreferTheEarlierStage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// The Revised Decision is consumed first; the Provisional one, equally
	// strict, arrives later and takes over.
	s.UpsertCases(ctx, []Decision{dec("d", 2, 2, .99)})
	need, _ := s.UpsertCases(ctx, []Decision{dec("d", 1, 2, .97)})
	var by int16
	var version string
	s.Pool.QueryRow(ctx, `SELECT flagged_by, model_version FROM cases WHERE event_id = 'd'`).Scan(&by, &version)
	if len(need) != 1 || by != 1 || version != "m1" {
		t.Fatalf("need %v, flagged_by %d, model %s; want a recompute, 1, m1", need, by, version)
	}
}
