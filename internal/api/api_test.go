package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kanishka-sahoo/unfraud/internal/store"
)

func testServer(t *testing.T, replays Replays) *httptest.Server {
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
	db := fmt.Sprintf("api_%d", time.Now().UnixNano())
	admin.Exec(ctx, "CREATE DATABASE "+db)
	cfg, _ := pgx.ParseConfig(url)
	st, err := store.Open(ctx, fmt.Sprintf("postgres://%s:%s@%s:%d/%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, db))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_500_000_000, 0)
	ds := []store.Decision{
		{Profile: "p", EventID: "e1", Stage: 1, EventTime: t0, Probability: .99, Action: 2, ModelAction: 2, ModelVersion: "m", ScoredAt: t0, IngestedAt: t0, Entities: map[string]string{"card": "c1"}},
		{Profile: "p", EventID: "e2", Stage: 1, EventTime: t0, Probability: .6, Action: 1, ModelAction: 1, ReasonCodes: []string{"R"}, ModelVersion: "m", ScoredAt: t0, IngestedAt: t0, Entities: map[string]string{"card": "c2"}},
		{Profile: "p", EventID: "e3", Stage: 1, EventTime: t0, Probability: .1, Action: 0, ModelVersion: "m", ScoredAt: t0, IngestedAt: t0},
	}
	fresh, _ := st.InsertDecisions(ctx, ds)
	st.UpsertCases(ctx, fresh)
	srv := httptest.NewServer(Handler(Config{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		GraphURL: func(string) string { return "http://127.0.0.1:1" }, Replays: replays}))
	t.Cleanup(func() {
		srv.Close()
		st.Close()
		admin.Exec(ctx, "DROP DATABASE "+db)
		admin.Close(ctx)
	})
	return srv
}

func get(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

func TestCasesQueueAndDetail(t *testing.T) {
	srv := testServer(t, nil)
	var cs []store.CaseListItem
	if get(t, srv.URL+"/api/cases?profile=p", &cs); len(cs) != 2 || cs[0].EventID != "e1" {
		t.Fatalf("queue %+v; want e1 then e2 (approved e3 is no Case)", cs)
	}
	if get(t, srv.URL+"/api/cases?profile=p&entity=card=c2", &cs); len(cs) != 1 || cs[0].EventID != "e2" {
		t.Fatalf("entity filter %+v", cs)
	}
	var d caseDetail
	if code := get(t, srv.URL+"/api/cases/p/e2", &d); code != 200 || d.Case.ReasonCodes[0] != "R" || len(d.Decisions) != 1 {
		t.Fatalf("detail %d %+v", code, d)
	}
	if code := get(t, srv.URL+"/api/cases/p/nope", nil); code != 404 {
		t.Fatalf("missing case: %d", code)
	}
	if code := get(t, srv.URL+"/api/cases?profile=p&sort=bogus", nil); code != 400 {
		t.Fatalf("bad sort: %d", code)
	}
	var o store.Overview
	if get(t, srv.URL+"/api/overview/p", &o); o.Cases.Open != 2 || o.Cases.OpenBlock != 1 {
		t.Fatalf("overview %+v", o.Cases)
	}
}

func TestDispositions(t *testing.T) {
	srv := testServer(t, nil)
	post := func(path, body string) int {
		res, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := post("/api/cases/p/e1/disposition", `{"disposition":"guilty"}`); c != 400 {
		t.Fatalf("invalid disposition: %d", c)
	}
	if c := post("/api/cases/p/nope/disposition", `{"disposition":"dismissed"}`); c != 404 {
		t.Fatalf("missing case: %d", c)
	}
	if c := post("/api/cases/p/e1/disposition", `{"disposition":"confirmed","note":"called the bank"}`); c != 200 {
		t.Fatalf("dispose: %d", c)
	}
	var cs []store.CaseListItem
	if get(t, srv.URL+"/api/cases?profile=p&status=disposed", &cs); len(cs) != 1 || *cs[0].Disposition != "confirmed" {
		t.Fatalf("disposed %+v", cs)
	}
}

type fakeReplays struct{ running bool }

func (f *fakeReplays) List(context.Context) ([]Replay, error) {
	if f.running {
		return []Replay{{Name: "r1", Status: "running"}}, nil
	}
	return []Replay{}, nil
}

func (f *fakeReplays) Start(_ context.Context, r ReplayRequest) (Replay, error) {
	f.running = true
	return Replay{Name: "r2", Profile: r.Profile, Status: "running"}, nil
}

func TestOneReplayAtATime(t *testing.T) {
	srv := testServer(t, &fakeReplays{})
	start := func() int {
		res, _ := http.Post(srv.URL+"/api/replays", "application/json", strings.NewReader(`{"profile":"tabformer","speed":100}`))
		res.Body.Close()
		return res.StatusCode
	}
	if c := start(); c != 200 {
		t.Fatalf("first replay: %d", c)
	}
	if c := start(); c != 409 {
		t.Fatalf("second replay while one runs: %d", c)
	}
}

func TestGraphUnavailable(t *testing.T) {
	srv := testServer(t, nil)
	if c := get(t, srv.URL+"/api/graph/p/c1", nil); c != 502 {
		t.Fatalf("graph-svc down: %d", c)
	}
}
