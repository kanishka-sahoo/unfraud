# unfraud: design

Status: draft, 2026-10-12. Terms in **bold** are defined in [`CONTEXT.md`](../CONTEXT.md); decisions link to [`adr/`](./adr).

## 1. Goals

- Score each **Event** within tens of milliseconds, using fast rules plus a gradient-boosted model over features that update in real time.
- Catch coordinated fraud that looks normal one Event at a time by linking **Entities** into a graph and raising **Ring Alerts**.
- Give an analyst a **Case** for every flag, including a grounded LLM **Summary**, in a multi-page web app.
- Run like a production system (horizontal scaling, observability, defined degraded modes), deployed to local Kubernetes.
- Publish every metric with a single command that anyone can rerun from public data.

**Non-goals:** a cloud deployment; training on analyst **Dispositions**; claiming accuracy on data we can't evaluate honestly (see §2).

## 2. Datasets ([ADR-0001](./adr/0001-dataset-roles.md))

| Profile | Role | Why |
|---|---|---|
| IEEE-CIS (Vesta) | **Main dataset for fraud metrics**, both model and attribute-graph | Real data; has velocity signals and shared attributes (device, email, address, card pieces) |
| Elliptic | **Graph metrics** through the full streaming pipeline | Real labeled Bitcoin flow graph (49 timesteps) |
| PaySim | **Load testing only** | Synthetic; balance columns give away the label; origin accounts almost never repeat |

What we claim is "reproducible from public data", not "real". Both Kaggle datasets forbid redistribution, so `make data` downloads them with the user's own Kaggle credentials and checks SHA-256 hashes.

Limitations we state openly:

- IEEE-CIS has no raw IP and no latitude/longitude. "Movement" means `addr1` changes and `dist1` deltas.
- IEEE-CIS test labels are private, so all evaluation uses a time split of the training set.
- IEEE-CIS labels are close to user-level, so random splits leak and are never used.
- Elliptic's 72 aggregated features are precomputed neighbor statistics. We report a local-features-only variant (94 features) and an all-features variant; graph lift is quoted from the local-only variant.
- Elliptic models degrade after a dark-market shutdown around timestep 43. We report that drop rather than hide it.

## 3. One pipeline, many Profiles ([ADR-0002](./adr/0002-dataset-profiles.md))

A **Profile** is configuration plus one adapter:

```
profiles/<name>/
  adapter       raw files → Event / Link / Watermark stream (Go, registered by name)
  entities      which Entities exist and how they're extracted
  links         attribute Link types (with Super-node caps) and/or flow Links
  features.yaml windowed features per Entity
  rules.yaml    rules with Reason Codes
  model         reference to a registry artifact
  labels        Label Release delay (Event Time)
```

Every message on the wire uses one Protobuf schema: `Event{profile, event_id, event_time, entities, num_features, cat_features}`, `Link{src, dst, event_time}`, `Watermark{event_time}`, `LabelRelease{event_id, label, release_time}`. Services contain no dataset-specific logic.

## 4. Architecture

```
                         Kubernetes (kind, local)
 replayer (Job) ─► events / links / watermarks / labels.released    (Kafka, Strimzi)
                         │
                         ▼
   scorer ×N  ──── Valkey (feature windows, graph stats, dedup)
     rules → LightGBM (leaves) → TreeSHAP → Decision
     ├─► scored
     └─► flagged ─────────────────────────────┐
                                               ▼
   graph-svc (single writer + standby)       case-svc ──► Postgres (CloudNativePG)
     union-find, neighbor aggregates, Leiden   ▲   │
     ├─► graph stats → Valkey                  │   ▼
     ├─► ring.alerts ──────────────────────────┤  summarizer ──► OpenRouter
     └─► revisions ─────────────────────────────┘   └─► summaries ─► case-svc
                                                         
   cases-api (Go, REST + SSE) ◄── Postgres, Kafka tail, Prometheus
        ▲
   web (Next.js)

 Every consumer: DLQ topic · OpenTelemetry traces · Prometheus metrics
 Model registry: S3-compatible store (SeaweedFS) + manifests in Postgres
```

### Services

| Service | Language | Scaling | Responsibility |
|---|---|---|---|
| `replayer` | Go | k8s Job, one per Replay | Runs a Profile adapter and emits in Event Time order at a speed multiplier (or as fast as possible). Emits Label Releases on the delayed schedule. |
| `scorer` | Go | Horizontal, KEDA on consumer lag | Feature update and read, rules, model, Contributions, **Provisional Score** and **Decision**, Shadow Model scoring. Stateless. |
| `graph-svc` | Go | Single writer + hot standby ([ADR-0007](./adr/0007-single-writer-graph-service.md)) | Builds Links, maintains Components and neighbor aggregates, runs Leiden on a window, emits **Ring Alerts** and **Revisions**. Snapshots to object store; rebuilds from compacted topics. |
| `case-svc` | Go | Horizontal | Opens and updates **Cases**, builds **Case Packets**, applies Revisions, stores everything in Postgres. |
| `summarizer` | Go | Horizontal, rate-limited | Turns Case Packet into **Summary** via OpenRouter, runs the Grounding check, retries, sends failures to the DLQ ([ADR-0009](./adr/0009-llm-via-openrouter-off-hot-path.md)). |
| `cases-api` | Go | Horizontal | REST (OpenAPI) + SSE for the web app. Analyst Dispositions. Replay control (creates replayer Jobs). Model promotion. |
| `web` | Next.js | Horizontal | The analyst and operator UI (§9). |
| `train` | Python CLI | Batch Job | Turns the feature Parquet into LightGBM/CatBoost models, evaluation, GraphSAGE baseline. Not part of the runtime. |

### Delivery semantics

Producers are idempotent; consumers process at-least-once. Feature updates are deduplicated on `event_id` so a redelivered Event doesn't count twice. Schemas live in a schema registry (Apicurio) and use buf-managed Protobuf.

## 5. Features ([ADR-0004](./adr/0004-event-time-and-label-release.md), [ADR-0005](./adr/0005-single-feature-implementation.md))

All windows are in **Event Time**. Each feature is declared in `features.yaml` and computed by one Go engine, which serves both training and live scoring.

IEEE-CIS families:

- **Velocity:** count and amount sum per Card UID over 1h/24h/7d; distinct Card UIDs per Device Fingerprint; distinct Device Fingerprints and emails per Card UID.
- **Unusual behavior:** amount ÷ the Card UID's running median; time since last seen; first time this Device Fingerprint and Card UID appear together.
- **Movement:** `addr1` change versus last value, `dist1` delta, short-interval address change.
- **Graph (read from Valkey, written by graph-svc):** Component size, distinct Card UIDs in the Component, 24h Component growth, released fraud Labels in the Component.

Elliptic: the local features, plus online neighbor aggregates (mean neighbor Provisional Score, released illicit Labels among neighbors, in/out degree, 2-hop fan-in/fan-out).

**Label Release** rule: nothing may read a Label before `event_time + delay`. Defaults are 30 days for IEEE-CIS and 5 timesteps for Elliptic; both are configurable and the values used are recorded in the results.

State lives in Valkey as time-bucketed counters, with Lua scripts that update and read atomically, pipelined per Event. If Valkey is unavailable, the scorer falls back to static features and marks the Decision degraded.

## 6. Scoring ([ADR-0003](./adr/0003-two-stage-scoring.md), [ADR-0008](./adr/0008-pure-go-runtime-inference.md))

1. **Rules** (`rules.yaml`): condition → block / add to score / force review, with a Reason Code. Rules stay only if the ablation shows they add something on top of the model.
2. **Model**: LightGBM served in-process through `leaves`. CatBoost is trained and reported, and served through ONNX Runtime only if it clearly wins.
3. **Contributions**: Go TreeSHAP, checked in CI against LightGBM `pred_contrib` (tolerance 1e-6), alongside a Go-vs-Python score parity test.
4. **Decision**: thresholds are set from the **Alert Budget** on the validation month.
5. **Revision**: graph-svc sends `revisions` when new structure changes an Event's graph features: an Elliptic Watermark closes a timestep, or a Ring Alert fires on IEEE-CIS. Revised Decisions update the Case; the Provisional Decision is never overwritten.

Latency budget (targets, to be measured): consume → Decision under 10 ms p99 inside the scorer, under 50 ms p99 from end to end.

## 7. Graph (rings)

- **Attribute Links** (IEEE-CIS): Entities sharing a Device Fingerprint, email or address. Values whose degree exceeds a cap are treated as **Super-nodes** and excluded. A fixed list says which attribute types may create Links.
- **Flow Links** (Elliptic): taken directly from the dataset.
- **Online**: incremental union-find over a sliding window gives Components; per-Component stats are pushed to Valkey.
- **Periodic**: Leiden on the windowed graph every few minutes of Event Time gives **Communities**. Each is scored on size, growth rate, attribute reuse and released-Label density. Those above a threshold become **Rings**, which emit Ring Alerts, which open Cases and trigger Revisions.
- **Ring ground truth (our own definition, published):** a Component of at least 2 distinct Card UIDs, connected only through non-Super-node attribute Links, in which most Events are labeled fraud.
- **Headline graph metric:** fraud the Ring Alerts catch *among Events whose Provisional Score was below threshold*, at a fixed analyst budget.
- **No GNN in serving** ([ADR-0012](./adr/0012-no-gnn-in-serving.md)). The `train` CLI produces a GraphSAGE baseline on Elliptic for comparison.

## 8. Cases and LLM Summaries ([ADR-0009](./adr/0009-llm-via-openrouter-off-hot-path.md))

- A **Case** is either one flagged Event or one Ring.
- The **Case Packet** holds the Event, Decision history, Reason Codes, top Contributions, Entity history and Ring context. Anonymized IEEE-CIS `V*` columns appear only as one combined "model signal", never with guessed meanings.
- **OpenRouter** (OpenAI-compatible) is the only LLM gateway. Model is chosen per Profile and per Case type; defaults are `anthropic/claude-haiku-4.5` for Event Cases and `anthropic/claude-sonnet-5` for Ring Cases, with an open-weights model available.
- **Structured output:** `json_schema` where the model supports it, a tool-calling fallback otherwise, and schema validation with one repair retry in every case.
- **Benchmark pinning:** `provider.order`, `allow_fallbacks: false`, temperature 0. Each Summary records the model and provider OpenRouter actually used.
- A **`stub` driver** produces template Summaries, so everything runs without an API key.
- **Grounding** check: numbers and Entities in the Summary must appear in the Case Packet. Failures are regenerated once, then dropped and counted.
- **Published:** Grounding pass rate, a human-graded sample (accuracy, usefulness, brevity), cost per 1k Cases, and p50/p99 latency, each per model. The Summaries themselves are published as artifacts because LLM output isn't bit-reproducible.
- The LLM never affects a Decision and is never in the scoring path.

## 9. Web app (Next.js)

Next.js App Router with TypeScript. Server components fetch from `cases-api`; live views subscribe to SSE. API types are generated from the `cases-api` OpenAPI spec (Go server through oapi-codegen, client through openapi-typescript), so the UI and the API can't drift apart. There is no auth: it runs locally only, behind the cluster ingress.

| Route | Purpose |
|---|---|
| `/` | **Overview**: throughput, flag rate, p99 latency, consumer lag, degraded-Decision rate, latest Ring Alerts, active Replay. |
| `/stream` | **Live Events**: virtualized SSE feed filterable by Profile and Decision, with Provisional → Revised changes highlighted. |
| `/cases` | **Case queue**: sort by score, amount or age; filter by Profile, type and Disposition. |
| `/cases/[id]` | **Case detail**: Summary (Grounding badge, model and provider used), score with a Contribution waterfall, Reason Codes, Decision timeline including Revisions, Entity velocity charts, mini Ring graph, Disposition actions. |
| `/rings` | **Ring list**: size, growth, Label density, status. |
| `/rings/[id]` | **Ring detail**: interactive WebGL graph (Sigma.js + graphology), member table, growth over Event Time, Ring Summary. |
| `/entities/[type]/[id]` | **Entity history**: every Event, Link and Case involving one Card UID, Device Fingerprint and so on. |
| `/models` | **Registry**: active and Shadow Models per Profile, parity and TreeSHAP check status, metrics, Shadow-vs-active agreement, promote action. |
| `/benchmarks` | **Results**: ablation tables, PR curves and system-run charts from `results/*.json`, with dataset hashes, hardware and git SHA. |
| `/replay` | **Replay control**: choose Profile, mode (Accuracy Run / System Run) and speed; start/stop; progress. |

**Dispositions** are stored but never become Labels and never feed metrics or training ([ADR-0011](./adr/0011-dispositions-are-not-labels.md)). Playwright runs end-to-end tests against the kind cluster with the `stub` LLM driver.

## 10. Benchmarking ([ADR-0006](./adr/0006-accuracy-vs-system-runs.md))

- **Accuracy Run**: deterministic Replay that pauses at each Watermark until graph-svc catches up, so features are exact as of Event Time. All fraud metrics come from this mode, and so does the training Parquet.
- **System Run**: production topology, N scorers, full speed. Reports throughput, p50/p99 latency, consumer lag, and score drift compared with the Accuracy Run (the measured cost of stale features).

IEEE-CIS results table (time split: months 1–4 train / 5 validation / 6 test). PR-AUC; recall and dollar-weighted recall at a 0.5% Alert Budget; extra fraud caught by Rings below threshold:

| Configuration | PR-AUC | Recall @ budget | $-recall @ budget | Ring lift |
|---|---|---|---|---|
| Rules only | | | | |
| Model, static features | | | | |
| + real-time features | | | | |
| + graph features | | | | |
| + Ring Alerts / Revisions | | | | |

Elliptic: illicit F1 on the timestep 35–49 test split for local-only and all-features variants, Provisional versus Revised, compared with Weber et al. 2019 and our GraphSAGE baseline, with a per-timestep chart around timestep 43.

PaySim: System Runs only.

We also report how much the IEEE-CIS results fall short of the Kaggle leaderboard and why: the leaderboard solutions aggregated over train plus test together, which a streaming system can't do.

`make bench` runs everything and writes `results/<run-id>.json`, including dataset hashes, git SHA, hardware, Label Release delays and LLM provider pins.

## 11. Deployment: local Kubernetes only ([ADR-0010](./adr/0010-local-kubernetes-only.md))

- **kind** cluster with a local image registry; **Tilt** for the inner dev loop; one umbrella **Helm** chart.
- **Operators for stateful parts:** Strimzi (Kafka in KRaft mode, 3 brokers), CloudNativePG (Postgres). Valkey and SeaweedFS run as plain StatefulSets.
- **Observability:** kube-prometheus-stack, OpenTelemetry Collector → Tempo, Grafana dashboards shipped in the chart.
- **KEDA** scales the scorer on Kafka consumer lag.
- Two values profiles: `full` (3 brokers, standby graph-svc, about 16 GB RAM target) and `lite` (1 broker, no standby, for smaller machines).
- Secrets (OpenRouter key, Kaggle credentials) are Kubernetes Secrets created by `make secrets`. Nothing leaves the machine except calls to OpenRouter.

## 12. Repo layout

```
cmd/            replayer, scorer, graph-svc, case-svc, summarizer, cases-api
internal/       profile/, features/, rules/, model/ (leaves, treeshap), graph/, llm/, kafka/, store/
profiles/       ieee-cis/, paysim/, elliptic/
proto/          buf module
api/            cases-api OpenAPI spec
web/            Next.js app
train/          Python CLI (uv): build-features, train, evaluate, gnn-baseline
deploy/         helm/, kind/, Tiltfile, grafana/
docs/           design.md, adr/
results/        published benchmark outputs
Makefile        data, secrets, up, train, bench-accuracy, bench-system, e2e
```

## 13. Milestones

1. **Data foundations:** Profiles, Protobuf schema, replayer, kind + Strimzi, checksummed `make data`.
2. **Features:** feature engine, Accuracy Run replay to Parquet, first IEEE-CIS model, parity tests in CI.
3. **Scoring:** scorer with rules, model, TreeSHAP and Decisions; first three rows of the results table.
4. **Graph:** graph-svc, Revisions, Rings, Elliptic end to end; remaining results rows.
5. **Cases:** case-svc, Postgres, summarizer with OpenRouter and the stub driver, Grounding metrics.
6. **Web app:** cases-api plus the Next.js app (cases and rings first, then overview, models, benchmarks, replay).
7. **Hardening:** KEDA, DLQs, degraded modes, Shadow Models, standby graph-svc, System Runs, dashboards, Playwright end-to-end tests.

## 14. Open questions

- Super-node degree cap and which attribute types may create Links: tune on validation data, and publish the values.
- Exact Card UID recipe for IEEE-CIS: use the published community recipe, and measure its collision rate.
- Whether Elliptic++ (wallet graph) or Elliptic2 (subgraph labels) is worth adding as a fourth Profile once Elliptic works.
