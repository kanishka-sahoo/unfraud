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

What we claim is "reproducible from public data", not "real". `make data` downloads everything with the user's own Kaggle credentials and checks SHA-256 hashes against `data/checksums.sha256`. Licences differ, and the data is never committed:

- **IEEE-CIS:** Kaggle competition rules; competition use only; no redistribution. Downloading needs accepted rules and a phone-verified Kaggle account.
- **PaySim:** CC BY-SA 4.0.
- **Elliptic:** CC BY-NC-ND 4.0. Publishing metrics is fine; publishing modified copies of the data is not.

**Event Time conventions.** None of the datasets give real calendar timestamps, so each adapter uses a documented base:

| Profile | Event Time |
|---|---|
| IEEE-CIS | 2017-11-30 + `TransactionDT` seconds (the Kaggle community's reference date) |
| PaySim | 2017-01-01 + `step` hours |
| Elliptic | 2015-12-30 + (timestep − 1) × 14 days, aligned so each timestep falls in its own watermark bucket |

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
  features      real-time features per Entity (Go values)
  rules.yaml    rules with Reason Codes
  model         reference to a registry artifact
  labels        Label Release delay (Event Time)
```

Every Profile has one ordered input topic, `input.<profile>`, carrying a Protobuf `Envelope` (`proto/unfraud/v1/input.proto`) whose payload is an `Event`, `Link`, `Watermark` or `LabelRelease` ([ADR-0013](./adr/0013-ordered-input-topic-and-feature-schema.md)):

- Events, Links and Label Releases are keyed by the primary Entity (Card UID, PaySim origin, Elliptic tx). Each Watermark is copied to every partition.
- Event features are positional: `repeated double num` (NaN = missing) and `repeated string cat` ("" = missing), in the order given by the Profile's **Feature Schema**. A `schema_id` hash lets consumers reject Events built against a different schema.
- Label Releases come from a second cursor over the dataset that trails the first by the label delay, so the replayer's memory stays constant (PaySim peaks at 2.9M pending Labels).

Services contain no dataset-specific logic.

## 4. Architecture

```
                         Kubernetes (kind, local)
 replayer (Job) ─► input.<profile>: Events, Links, Watermarks, Label Releases   (Kafka, Strimzi)
                         │
         ┌───────────────┴──────────────────────────┐
         ▼                                          ▼
   scorer ×N (partition-parallel)             graph-svc (single writer)
     features ⇄ Valkey (one round trip)         exact in-memory features + graph
     model → rules → Provisional Decision       graph/flow model → Revised Decision
     └─► scored.<profile>                       └─► revised.<profile>
                         │                          │
                         └──────────┬───────────────┘
                                    ▼
                     case-svc (stricter of the two; TreeSHAP) ──► Postgres (CloudNativePG)
                                    │
                         summarizer ──► OpenRouter
                                    │
   cases-api (Go, REST + SSE) ◄── Postgres, Kafka tail, Prometheus
        ▲
   web (Next.js)

 Every consumer: DLQ topic · Prometheus metrics
 Models ship as OCI images (ADR-0015); graph-svc recovers by re-reading its input
```

### Services

| Service | Language | Scaling | Responsibility |
|---|---|---|---|
| `replayer` | Go | k8s Job, one per Replay | Runs a Profile adapter and emits in Event Time order at a speed multiplier (or as fast as possible). Emits Label Releases on the delayed schedule. |
| `scorer` | Go | Horizontal by partitions (one replica in `lite`) | One worker per assigned partition: feature exchange with Valkey (one round trip per Event), model, rules, **Provisional Score** and **Decision**, Shadow Model scoring. Writes `scored.<profile>`. Stateless; Contributions are left to case-svc. |
| `graph-svc` | Go | Single writer ([ADR-0007](./adr/0007-single-writer-graph-service.md)) | Reads every partition in Event Time order with exact in-memory feature and graph state, and writes **Revised** Decisions to `revised.<profile>` with a graph-aware model ([ADR-0017](./adr/0017-graph-svc-revises-instead-of-serving-graph-features.md)). Recovers by re-reading the input topic. |
| `case-svc` | Go | Horizontal | Opens and updates **Cases**, computes **Contributions** (Go TreeSHAP) for flagged Events from the model input the scorer attaches, builds **Case Packets**, applies Revisions, stores everything in Postgres. |
| `summarizer` | Go | Horizontal, rate-limited | Turns Case Packet into **Summary** via OpenRouter, runs the Grounding check, retries, sends failures to the DLQ ([ADR-0009](./adr/0009-llm-via-openrouter-off-hot-path.md)). |
| `cases-api` | Go | Horizontal | REST (OpenAPI) + SSE for the web app. Analyst Dispositions. Replay control (creates replayer Jobs). Model promotion. |
| `web` | Next.js | Horizontal | The analyst and operator UI (§9). |
| `train` | Python CLI | Batch Job | Turns the feature Parquet into LightGBM models and evaluation reports. Not part of the runtime. |

### Delivery semantics

Producers are idempotent; consumers process at-least-once. Every Event's and Label Release's feature updates carry a Guard (`e|<event_id>`, `l|<event_id>`) that Valkey checks with `SET NX` inside the same Lua script, so a redelivered record never counts twice (it is still scored, against state that already includes it). Guards expire 7 days later in Event Time. Consumers of `scored.<profile>` must deduplicate on `event_id`. Schemas are buf-managed Protobuf compiled into every service, with no runtime schema registry; `buf breaking` in CI is the compatibility gate ([ADR-0014](./adr/0014-no-schema-registry.md)).

## 5. Features ([ADR-0004](./adr/0004-event-time-and-label-release.md), [ADR-0005](./adr/0005-single-feature-implementation.md))

All windows are in **Event Time**. Each feature is declared on its Profile as a `profile.Feature` value (Go, type-checked; no YAML) and computed by one Go engine (`internal/features`), which serves both training and live scoring. Features always describe the Entity's history **before** the Event being scored.

Windowed counts and sums are bucketed: a window of length W is read as the 24 buckets of W/24 ending with the current one, so it reaches back between 23/24·W and W. Distinct counts are exact sliding windows (last-seen time per member). Engine state goes through a batched `Get`/`Apply` interface, so the Valkey implementation needs one read and one write round trip per Event.

IEEE-CIS families:

- **Velocity:** count and amount sum per Card UID over 1h/24h/7d; distinct Card UIDs per Device Fingerprint; distinct Device Fingerprints and emails per Card UID.
- **Unusual behavior:** amount ÷ the Card UID's running median; time since last seen; first time this Device Fingerprint and Card UID appear together.
- **Movement:** `addr1` change versus last value, `dist1` delta, short-interval address change.
- **Graph (read from Valkey, written by graph-svc):** Component size, distinct Card UIDs in the Component, 24h Component growth, released fraud Labels in the Component.

Elliptic: the local features, plus online neighbor aggregates (mean neighbor Provisional Score, released illicit Labels among neighbors, in/out degree, 2-hop fan-in/fan-out).

**Label Release** rule: nothing may read a Label before `event_time + delay`. Defaults are 30 days for IEEE-CIS and 5 timesteps for Elliptic; both are configurable and the values used are recorded in the results.

State lives in Valkey as time-bucketed counters, with Lua scripts that update and read atomically, pipelined per Event. If Valkey is unavailable, the scorer falls back to static features and marks the Decision degraded.

## 6. Scoring ([ADR-0003](./adr/0003-two-stage-scoring.md), [ADR-0008](./adr/0008-pure-go-runtime-inference.md))

1. **Rules** (`profile.Rule` values): all conditions hold → review or block, with a Reason Code. Rules only escalate the model's Decision. They are written before results and reviewed on the validation month only: a Rule that catches no fraud there is retired (kept in code with the evidence).
2. **Model**: LightGBM served in-process by our own Go evaluator (`internal/model/lgbm`; `leaves` can't read LightGBM 4.x models). CatBoost was planned as an offline comparison but has not been trained or evaluated.
3. **Contributions**: Go TreeSHAP, checked in CI against LightGBM `pred_contrib` (tolerance 1e-9), alongside the Go-vs-Python score parity tests (fixtures in CI, real data with `make parity`). TreeSHAP costs about trees × leaves × depth², so it runs in case-svc for flagged Events only, not in the scoring path. Models are trained with `max_depth=10`: on IEEE-CIS that costs about 1% PR-AUC and makes scoring 19× faster (21 µs) and TreeSHAP 12× faster (16 ms).
4. **Decision**: thresholds are set from the **Alert Budget** on the validation month.
5. **Revision**: graph-svc sends `revisions` when new structure changes an Event's graph features: an Elliptic Watermark closes a timestep, or a Ring Alert fires on IEEE-CIS. Revised Decisions update the Case; the Provisional Decision is never overwritten.

Latency budget (targets, to be measured): consume → Decision under 10 ms p99 inside the scorer, under 50 ms p99 from end to end.

## 7. Graph (rings)

- **Attribute graph** (IEEE-CIS, `internal/graph`): cards link through shared Device Fingerprints only. Chosen on the training months: devices shared by 2–20 cards carry 9–14% fraud against 3.5% overall, while email domains and addresses are almost all Super-nodes. A device used by more than 20 cards in the 30-day window is a **Super-node** and never links. The graph is rebuilt daily in Event Time (union-find cannot delete edges) and only grows in between.
- **Communities**: chains of shared devices still merge up to 4,336 cards into one Component, so each rebuild runs a deterministic Louvain (largest Community 749 cards). Features describe both the Component and the Community the Event joins, including other cards' released fraud Labels.
- **Rings and Ring Alerts**: a Community with at least 3 cards, 2 of them with released fraud, flags its cards' later Events and revises their Events approved in the previous 7 days. On IEEE-CIS every policy tried lost to the model at equal alert volume on the validation month (5.1% vs 18.9% precision), so Ring Alerts are **off** for IEEE-CIS; the graph contributes through its features (PR-AUC 0.611 → 0.625). The mechanism stays for datasets where it pays.
- **Flow graph** (Elliptic): payment Links within each timestep, complete when a Watermark closes it. At close the stream computes in/out degree, 2-hop reach and neighbour means of the 93 local features for **Revisions**. These recover the value of the dataset's 72 precomputed aggregates (PR-AUC 0.793 vs 0.792) and lift illicit F1 from 0.734 (Provisional) to 0.780 (Revised).
- **No GNN in serving** ([ADR-0012](./adr/0012-no-gnn-in-serving.md)).

## 8. Cases and LLM Summaries ([ADR-0009](./adr/0009-llm-via-openrouter-off-hot-path.md))

- **case-svc** (one per Profile) consumes `scored.<profile>` and `revised.<profile>`, stores every Decision (`decisions` table, redeliveries ignored), and opens a **Case** for each flagged Event. A Case keeps the stricter of its Provisional and Revised Decisions and is never made less strict. When a Case opens or becomes stricter, case-svc computes **Contributions** (Go TreeSHAP) with the model that flagged it, matched by model version; it holds both the scorer's and graph-svc's models.
- The **Case Packet** (`internal/cases`) holds the Decision, rule Reason Codes with descriptions, Entities, and the strongest model factors with plain-language descriptions generated from each feature's declaration. Masked columns (IEEE-CIS V/C/D/M/id_*, Elliptic local_*) are combined into one "anonymized features" effect and never described individually.
- The **summarizer** claims Cases from a Postgres queue (`FOR UPDATE SKIP LOCKED`, strictest and highest-scoring first, optional rate limit), which is simpler than a Kafka topic for a few thousand Cases and survives restarts (stale claims are requeued).
- **Drivers**: `stub` (deterministic template, no key, grounded by construction) and `openrouter` (OpenAI-compatible API, `json_schema` output, temperature 0, optional provider pinning with fallbacks off, retries on 429/5xx; the model, provider, tokens and cost OpenRouter reports are stored with each Summary).
- **Grounding** (`cases.Ground`): every number in a Summary must be a rounding of a Packet value or a percentage of one (whole numbers up to 10 are allowed for counting words), and every identifier-like token must appear verbatim. An ungrounded Summary is retried once, then stored as `failed` and never shown.
- **Published** by `scripts/summary-report.sh`: Grounding pass rate, retries, errors, p50/p99 latency and cost per 1k Cases per model, plus a random sample for human grading.
- The LLM never affects a Decision and is never in the scoring path.

## 9. Web app (Next.js)

Next.js 16 (App Router, Cache Components: every page ships a static shell and streams its data behind `<Suspense>`) with TypeScript. Server components call `cases-api` inside the cluster; browser calls (live stream, dispositions, replays, graph) go through one route-handler proxy, so the browser talks to a single origin. API types are generated from `api/openapi.yaml` with openapi-typescript, and CI fails if they drift. There is no auth: the console is local-only.

Visual direction: intaglio-ink navy with paper-coloured text; decisions are always an icon plus a word; the analyst memo (the LLM Summary) is set in a serif because it is prose; chart colours were validated for colour-vision deficiency against both navy surfaces.

| Route | Purpose |
|---|---|
| `/{profile}` | **Overview**: Events scored, flag rates per Stage, open Cases, Summaries, Decisions per minute (zero-filled last hour), newest Cases. |
| `/{profile}/live` | **Live Decisions** over server-sent events (at most 20 a second per browser, with a count of the rest). |
| `/{profile}/cases` | **Case queue**: status, strictness and order filters in the URL; entity filter. |
| `/{profile}/cases/{id}` | **Case detail**: the Summary with its Grounding note and model, provider, latency and cost; the **risk ledger** (Contributions opening at the model's base rate and closing at the Case's probability, with a running probability per factor; masked features labelled as such); the Provisional and Revised Decisions; Entities (each a link to that Entity's Cases); Disposition. |
| `/{profile}/graph/{node}` | **Graph explorer** (Sigma.js + ForceAtlas2): a card's shared-device neighbourhood or a transaction's payment neighbourhood, served from graph-svc's live state; released fraud marked. It replaces the planned Ring pages, since Ring Alerts are off for IEEE-CIS (§7). |
| `/models` | Model versions in use per Profile and Stage, from the Decisions they made. |
| `/benchmarks` | The committed `results/` (embedded in cases-api), plus Elliptic F1 per test timestep. |
| `/replay` | Start a replay Job (cases-api creates it through the Kubernetes API with a namespaced Role) and see recent ones. |

**Dispositions** are stored but never become Labels and never feed metrics or training ([ADR-0011](./adr/0011-dispositions-are-not-labels.md)). Playwright tests (`web/e2e`) run against the deployed console.

## 10. Benchmarking ([ADR-0006](./adr/0006-accuracy-vs-system-runs.md))

- **Accuracy Run**: deterministic Replay that pauses at each Watermark until graph-svc catches up, so features are exact as of Event Time. All fraud metrics come from this mode, and so does the training Parquet.
- **System Run**: production topology, N scorers, full speed. Reports throughput, p50/p99 latency, consumer lag, and score drift compared with the Accuracy Run (`cmd/drift`: the measured cost of concurrency). Scorer partitions are aligned in Event Time ([ADR-0016](./adr/0016-event-time-alignment-across-partitions.md)); without that, a backlog let partitions drift apart and 96% of scores differed.

IEEE-CIS results table (time split: months 1–4 train / 5 validation / 6 test). PR-AUC, plus recall at Alert Budgets of 0.5%, 1%, 2% and 5% of Events, with dollar-weighted recall and precision at 5%; extra fraud caught by Rings below threshold. Each budget's threshold is set on the validation month and applied unchanged to the test month.

Several budgets are needed because fraud is 3.5% of IEEE-CIS Events: a 0.5% budget can catch at most about 14% of fraud, so recall there measures the ceiling, not the model. Every table also prints those ceilings. Tables are generated by `unfraud-train report` into `results/`.

| Configuration | PR-AUC | Recall @ budget | $-recall @ budget | Ring lift |
|---|---|---|---|---|
| Rules only | | | | |
| Model, static features | | | | |
| + real-time features | | | | |
| + graph features | | | | |
| + Ring Alerts / Revisions | | | | |

Elliptic: illicit F1 on the timestep 35–49 test split for local-only and all-features variants, Provisional versus Revised, compared with Weber et al. 2019 (Random Forest and GCN), with a per-timestep chart around timestep 43.

PaySim: System Runs only.

We also report how much the IEEE-CIS results fall short of the Kaggle leaderboard and why: the leaderboard solutions aggregated over train plus test together, which a streaming system can't do.

`make bench` runs everything and writes `results/<run-id>.json`, including dataset hashes, git SHA, hardware, Label Release delays and LLM provider pins.

## 11. Deployment: local Kubernetes only ([ADR-0010](./adr/0010-local-kubernetes-only.md))

What is built and runs on the 4-core, 8 GB reference machine (`lite`, the default values):

- **kind** single-node cluster (`make cluster-up`), images loaded with `kind load` rather than a registry, one Helm chart (`make deploy`, `make deploy-model`).
- **Operators:** Strimzi (Kafka 4.3 in KRaft mode, one broker with a 512 MB heap) and CloudNativePG (one Postgres instance). Valkey is a plain StatefulSet with append-only persistence and `noeviction`; its expiry is in Event Time, never wall-clock TTLs.
- **Services:** scorer and graph-svc per Profile (one replica each), case-svc per Profile, one summarizer, cases-api, and the web console (NodePort 30080 on new clusters, `make console` on older ones).
- **Failure handling:** idempotent producers, at-least-once consumers with idempotency Guards on feature state; records that cannot be processed go to `dlq.<profile>` with the error and origin in headers; when Valkey is unreachable the scorer keeps deciding with real-time features missing and marks Decisions `degraded`; graph-svc recovers by re-reading its input. An optional **Shadow Model** scores every Event beside the active one (recorded, never acted on).
- **Observability:** Prometheus (pods opt in with `prometheus.io/scrape`, 12-hour retention) and Grafana with a provisioned dashboard (`make grafana`): throughput and latency per Stage, degraded and dead-lettered records, Shadow agreement, graph-svc progress, case-svc and summarizer. Kafka and Valkey internals are not scraped.
- Every pod has a memory limit. With the full IEEE-CIS pipeline running, the cluster uses about 3.7 GB.
- Secrets (the OpenRouter key) are Kubernetes Secrets created by `make secrets`. Nothing leaves the machine except calls to OpenRouter.
- Model training runs outside the cluster.

Not built, because none of it could be validated on the reference machine: KEDA autoscaling, a hot-standby graph-svc (graph-svc is a single replica with `Recreate` rollouts and recovers by recomputation instead), OpenTelemetry tracing, and Tilt. `values-full.yaml` raises broker count and partitions but has not been run. Published System Run numbers come from `lite` on the reference machine.

## 12. Repo layout

```
cmd/            replayer, featurize, accuracy, scorer, graphsvc, casesvc, summarizer, casesapi, drift
internal/       profile/ (+ ieeecis, paysim, elliptic, registry), features/, graph/, rules/, scoring/,
                model/ (lgbm evaluator + TreeSHAP), align/, replay/, store/, cases/, llm/, api/, dlq/, table/, csvx/
proto/          buf module (input and scored schemas); gen/ holds the generated Go
api/            cases-api OpenAPI spec
web/            Next.js console and Playwright tests
train/          Python CLI (uv): train, report, fixture
deploy/         docker/, helm/unfraud (chart, values, Grafana dashboard), kind/
scripts/        fetch-data, system-run, summary-report, console-local
results/        published results (embedded in cases-api)
docs/           design.md, adr/
```

## 13. Milestones

All seven are done; see `git log` for what each delivered.

1. **Data foundations:** Profiles, Protobuf schema, replayer, kind + Strimzi, checksummed `make data`.
2. **Features:** feature engine, Accuracy Run replay to Parquet, first IEEE-CIS model, parity tests in CI.
3. **Scoring:** scorer with rules, model, TreeSHAP and Decisions; first three rows of the results table.
4. **Graph:** graph-svc, Revisions, Rings, Elliptic end to end; remaining results rows.
5. **Cases:** case-svc, Postgres, summarizer with OpenRouter and the stub driver, Grounding metrics.
6. **Web app:** cases-api plus the Next.js app (cases and rings first, then overview, models, benchmarks, replay).
7. **Hardening:** dead-letter topics, degraded mode, Shadow Models, Prometheus and Grafana, System Runs and paced latency, Playwright end-to-end tests. (KEDA and a standby graph-svc were not built; see §11.)

## 14. Open questions

- Super-node degree cap and which attribute types may create Links: tune on validation data, and publish the values.
- Exact Card UID recipe for IEEE-CIS: use the published community recipe, and measure its collision rate.
- Whether Elliptic++ (wallet graph) or Elliptic2 (subgraph labels) is worth adding as a fourth Profile once Elliptic works.
