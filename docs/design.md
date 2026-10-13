# unfraud: design

Status: draft, 2026-10-12. Terms in **bold** are defined in [`CONTEXT.md`](../CONTEXT.md); decisions link to [`adr/`](./adr).

## 1. Goals

- Score each **Event** within tens of milliseconds, using fast rules plus a gradient-boosted model over features that update in real time.
- Catch coordinated fraud that looks normal one Event at a time by linking **Entities** into a graph and raising **Ring Alerts**.
- Read every dataset into one **Payment Schema**, so features mean the same everywhere and a model trained on one dataset can score another.
- Give an analyst a **Case** for every flag, including a grounded LLM **Summary**, in a multi-page web app.
- Run like a production system (horizontal scaling, observability, defined degraded modes), deployed to local Kubernetes.
- Publish every metric with a single command that anyone can rerun from public data.

**Non-goals:** a cloud deployment; training on analyst **Dispositions**; claiming accuracy on data we can't evaluate honestly (see §2).

## 2. Datasets ([ADR-0018](./adr/0018-named-payment-datasets.md))

| Profile | Dataset | Role |
|---|---|---|
| `tabformer` | IBM TabFormer: 2,000 cardholders with several cards each, October 2014 to December 2019 (9.0M Events, 0.13% fraud) | Card fraud; attribute graph |
| `sparkov` | Sparkov (Kaggle): 1,000 cardholders, 693 merchants, 2019–2020 (1.85M Events, 0.52% fraud) | Card fraud |
| `amlworld` | IBM AMLworld HI-Small: 500,000 bank accounts over ten days (5.08M Events, 0.09% laundering) | Laundering as fraud; attribute graph |
| `paysim` | PaySim | **Load testing only**: balance columns give away the label; origin accounts almost never repeat |

Every column in these datasets has a published meaning. That is why they replaced IEEE-CIS and Elliptic, whose features were mostly masked. They are all simulations, so we claim "reproducible from public, synthetic data": metrics say how well the engine finds each simulator's fraud, not how it would do on a real bank's.

**The Payment Schema.** `unfraud-train prepare` (`train/src/unfraud_train/prepare.py`) turns each raw dataset into `data/raw/<profile>/events.csv`, sorted by Event Time:

| Column | TabFormer | Sparkov | AMLworld |
|---|---|---|---|
| `payer` (**Payer**) | user + card index | card number | bank + account paying |
| `payee` (**Payee**) | merchant | merchant | bank + account paid |
| `customer` (**Customer**) | user | card number (one card each) | the paying account's legal entity |
| `amount_usd` | amount | amount | amount paid ÷ the simulator's fixed exchange rate |
| `channel` | `in_person` or `online` | `in_person`/`online` from the `_pos`/`_net` category suffix, else missing | payment format (ACH, wire, cheque, cash, credit card, Bitcoin, reinvestment) |
| `category` | MCC mapped onto Sparkov's 11 categories | category without its suffix | missing |
| extras | entry mode, MCC, authorization errors, merchant state, abroad, out of home state, card brand/type/chip/limit, age, income, FICO | gender, home state, city population, job, age, distance to merchant | currencies, cross-currency, self-transfer, same bank, same owner |

`make data` downloads with the user's own Kaggle credentials, prepares, and checks SHA-256 hashes of both raw and prepared files against `data/checksums.sha256`. Licences: Sparkov CC0; AMLworld CDLA-Sharing-1.0; TabFormer as released by IBM (Apache-2.0 on GitHub; the Kaggle mirror says "other"); PaySim CC BY-SA 4.0. The data is never committed.

**Event Time.** The datasets carry calendar dates, read as UTC. Two are adjusted:

- Sparkov's date strings repeat 2019-02-28 for the simulator's 2012 leap day, so they are out of order. Event Time is `unix_time` shifted to match the strings from March 2019 on, which places January and February 2019 one day early.
- AMLworld is not sorted, and after ten days of normal traffic the simulator only finishes laundering attempts (1,108 transfers, 59% laundering). Those are dropped.

**Splits** are by Event Time and declared on each Profile (`profile.Split`):

| Profile | Warm-up | Train | Validation | Test |
|---|---|---|---|---|
| tabformer | Oct–Dec 2014 | 2015–2017 | 2018 | 2019 |
| sparkov | | Jan 2019 – Mar 2020 | Apr–Jun 2020 | Jul–Dec 2020 |
| amlworld | | 1–6 Sep 2022 | 7–8 Sep | 9–10 Sep |

Limitations we state openly:

- No Entity appears in more than one dataset, so nothing links across datasets. "Across datasets" means shared features and models that transfer.
- Sparkov's merchant coordinates are drawn near the cardholder at random, so `merchant_km` says little. Every Sparkov merchant is used by hundreds of cards, so it has no graph.
- AMLworld covers ten days, so its Label delay is 3 days, not the months real laundering investigations take.
- TabFormer fraud comes in bursts: 2017 has 255 fraud Events, 2016 has 3,579.

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

- Events and Label Releases are keyed by the primary Entity (the Payer; PaySim's origin). Each Watermark is copied to every partition.
- Event features are positional: `repeated double num` (NaN = missing) and `repeated string cat` ("" = missing), in the order given by the Profile's **Feature Schema**. A `schema_id` hash lets consumers reject Events built against a different schema.
- Label Releases come from a second cursor over the dataset that trails the first by the label delay, so the replayer's memory stays constant (PaySim peaks at 2.9M pending Labels).

Services contain no dataset-specific logic.

## 4. Architecture

```
                         Kubernetes (kind, local)
 replayer (Job) ─► input.<profile>: Events, Watermarks, Label Releases   (Kafka, Strimzi)
                         │
         ┌───────────────┴──────────────────────────┐
         ▼                                          ▼
   scorer ×N (partition-parallel)             graph-svc (single writer)
     features ⇄ Valkey (one round trip)         exact in-memory features + graph
     model → rules → Provisional Decision       graph model → Revised Decision
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

Every payment Profile computes the same real-time features (`payments.CoreFeatures`), under the same names:

- **Velocity:** count and amount per Payer over 1h/24h/7d; distinct Payees per Payer (24h/7d); count per Payee (1h/24h); distinct Payers per Payee (24h/7d); distinct Payers per Customer (7d).
- **Unusual behaviour:** amount ÷ the Payer's 30-day mean and ÷ the Payee's 7-day mean; seconds since the Payer's and the Payee's previous Event; first payment from this Payer to this Payee; channel or category different from the Payer's previous Event.
- **Released Labels:** fraud Labels on the Payer (30d/90d), the Payee (30d) and the Customer (90d).
- **Graph** (TabFormer, AMLworld; §7): the Component and Community the Payer joins, and released fraud in them.

With the core raw features (amount in USD, channel, category, hour, weekday) these are the **Core Features**. A model that uses only them gets schema ID 0 and binds to any payment Profile by name.

**Label Release** rule: nothing may read a Label before `event_time + delay`. The delay is 30 days for TabFormer and Sparkov and 3 days for AMLworld, configurable, and recorded in the results.

State lives in Valkey as time-bucketed counters, with Lua scripts that update and read atomically, pipelined per Event. If Valkey is unavailable, the scorer falls back to static features and marks the Decision degraded.

## 6. Scoring ([ADR-0003](./adr/0003-two-stage-scoring.md), [ADR-0008](./adr/0008-pure-go-runtime-inference.md))

1. **Rules** (`profile.Rule` values): all conditions hold → review or block, with a Reason Code. Rules only escalate the model's Decision. They are written before results and reviewed on the validation period only: a Rule less precise there than the model's top 2% (the review budget) is retired, kept in code with the evidence. A Rule only escalates, so a less precise one only adds alerts the model would have ranked lower.
2. **Model**: LightGBM served in-process by our own Go evaluator (`internal/model/lgbm`; `leaves` can't read LightGBM 4.x models). CatBoost was planned as an offline comparison but has not been trained or evaluated.
3. **Contributions**: Go TreeSHAP, checked in CI against LightGBM `pred_contrib` (tolerance 1e-9), alongside the Go-vs-Python score parity tests (fixtures in CI, real data with `make parity`). TreeSHAP costs about trees × leaves × depth², so it runs in case-svc for flagged Events only, not in the scoring path. Models are trained with `max_depth=10`, which kept scoring at about 21 µs and TreeSHAP at about 16 ms per Event on the earlier IEEE-CIS models.
4. **Decision**: thresholds are set from the **Alert Budget** on the validation period.
5. **Revision**: graph-svc re-scores Events with the graph model, and Ring Alerts (when enabled) revise recently approved Events. Revised Decisions update the Case; the Provisional Decision is never overwritten.

Latency budget (targets, to be measured): consume → Decision under 10 ms p99 inside the scorer, under 50 ms p99 from end to end.

## 7. Graph (rings)

- **Attribute graph** (`internal/graph`): Payers link through Payees they both paid. A Payee paid by more than the Profile's cap within the window is a **Super-node** and never links. Caps were chosen on training data only, from the fraud rate by Payee degree:
  - TabFormer: merchants used by 21–100 cards in 30 days carry 3.2× the base fraud rate; more popular ones 0.8×. Cap 100, window 30 days.
  - AMLworld: accounts paid by 6–20 others in a week carry 7.2× the base laundering rate (fan-in); those paid by more than 100 carry none. Cap 20, window 7 days.
  - Sparkov: no graph; every merchant is paid by hundreds of cards.
- The graph is rebuilt daily in Event Time (union-find cannot delete edges) and only grows in between. Each rebuild runs a deterministic Louvain; features describe both the Component and the Community the Event joins, including other Payers' released fraud Labels.
- **Rings and Ring Alerts**: a Community with at least 3 Payers, 2 of them with released fraud, flags its Payers' later Events and revises their Events approved in the previous 7 days. Off unless a Profile enables it after beating the model at equal alert volume on validation.
- **No GNN in serving** ([ADR-0012](./adr/0012-no-gnn-in-serving.md)).

## 8. Cases and LLM Summaries ([ADR-0009](./adr/0009-llm-via-openrouter-off-hot-path.md))

- **case-svc** (one per Profile) consumes `scored.<profile>` and `revised.<profile>`, stores every Decision (`decisions` table, redeliveries ignored), and opens a **Case** for each flagged Event. A Case keeps the stricter of its Provisional and Revised Decisions and is never made less strict. When a Case opens or becomes stricter, case-svc computes **Contributions** (Go TreeSHAP) with the model that flagged it, matched by model version; it holds both the scorer's and graph-svc's models.
- The **Case Packet** (`internal/cases`) holds the Decision, rule Reason Codes with descriptions, Entities, and the strongest model factors with plain-language descriptions generated from each feature's declaration. Every raw feature has a published meaning in the Profile's Glossary, so every factor is described. (A feature without one would be combined into a single "anonymized features" effect rather than guessed at.)
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
| `/{profile}/graph/{node}` | **Graph explorer** (Sigma.js + ForceAtlas2): a Payer's neighbourhood through shared Payees, served from graph-svc's live state; released fraud marked. It replaces the planned Ring pages, since Ring Alerts are off (§7). |
| `/models` | Model versions in use per Profile and Stage, from the Decisions they made. |
| `/benchmarks` | The committed `results/` (embedded in cases-api). |
| `/replay` | Start a replay Job (cases-api creates it through the Kubernetes API with a namespaced Role) and see recent ones. |

**Dispositions** are stored but never become Labels and never feed metrics or training ([ADR-0011](./adr/0011-dispositions-are-not-labels.md)). Playwright tests (`web/e2e`) run against the deployed console.

## 10. Benchmarking ([ADR-0006](./adr/0006-accuracy-vs-system-runs.md))

- **Accuracy Run**: deterministic Replay that pauses at each Watermark until graph-svc catches up, so features are exact as of Event Time. All fraud metrics come from this mode, and so does the training Parquet.
- **System Run**: production topology, N scorers, full speed. Reports throughput, p50/p99 latency, consumer lag, and score drift compared with the Accuracy Run (`cmd/drift`: the measured cost of concurrency). Scorer partitions are aligned in Event Time ([ADR-0016](./adr/0016-event-time-alignment-across-partitions.md)); without that, a backlog let partitions drift apart and 96% of scores differed.

Per Profile (`make train report`): PR-AUC and recall at Alert Budgets of 0.1%, 0.5%, 1% and 2% of Events, with dollar-weighted recall and precision at 0.5%, for the raw features, + real-time, + graph, and Core Features only. Each budget's threshold is set on the validation period and applied unchanged to the test period, and each table prints the recall a perfect model could reach at each budget.

Across Profiles (`make pooled`): Core Models trained on each dataset alone, on both card datasets pooled, and on all three, each scored on every dataset's test period. Datasets a model never saw are scored unchanged, with thresholds from that dataset's validation period. Pooled models are always reported per dataset, never as one pooled number (ADR-0018).

PaySim: System Runs only.

`make bench` runs everything and writes `results/<run-id>.json`, including dataset hashes, git SHA, hardware, Label Release delays and LLM provider pins.

## 11. Deployment: local Kubernetes only ([ADR-0010](./adr/0010-local-kubernetes-only.md))

What is built and runs on the 4-core, 8 GB reference machine (`lite`, the default values):

- **kind** single-node cluster (`make cluster-up`), images loaded with `kind load` rather than a registry, one Helm chart (`make deploy`, `make deploy-model`).
- **Operators:** Strimzi (Kafka 4.3 in KRaft mode, one broker with a 512 MB heap) and CloudNativePG (one Postgres instance). Valkey is a plain StatefulSet with append-only persistence and `noeviction`; its expiry is in Event Time, never wall-clock TTLs.
- **Services:** scorer and graph-svc per Profile (one replica each), case-svc per Profile, one summarizer, cases-api, and the web console (NodePort 30080 on new clusters, `make console` on older ones).
- **Failure handling:** idempotent producers, at-least-once consumers with idempotency Guards on feature state; records that cannot be processed go to `dlq.<profile>` with the error and origin in headers; when Valkey is unreachable the scorer keeps deciding with real-time features missing and marks Decisions `degraded`; graph-svc recovers by re-reading its input. An optional **Shadow Model** scores every Event beside the active one (recorded, never acted on).
- **Observability:** Prometheus (pods opt in with `prometheus.io/scrape`, 12-hour retention) and Grafana with a provisioned dashboard (`make grafana`): throughput and latency per Stage, degraded and dead-lettered records, Shadow agreement, graph-svc progress, case-svc and summarizer. Kafka and Valkey internals are not scraped.
- Every pod has a memory limit. With a full pipeline running, the cluster uses about 3.7 GB.
- What fits in `lite`: Sparkov end to end, and TabFormer's scorer and graph-svc. AMLworld does not: its feature state for 500,000 accounts took 5 GB in the Accuracy Run, against Valkey's 384 MB and graph-svc's 768 MiB here, so its numbers are offline only.
- Secrets (the OpenRouter key) are Kubernetes Secrets created by `make secrets`. Nothing leaves the machine except calls to OpenRouter.
- Model training runs outside the cluster.

Not built, because none of it could be validated on the reference machine: KEDA autoscaling, a hot-standby graph-svc (graph-svc is a single replica with `Recreate` rollouts and recovers by recomputation instead), OpenTelemetry tracing, and Tilt. `values-full.yaml` raises broker count and partitions but has not been run. Published System Run numbers come from `lite` on the reference machine.

## 12. Repo layout

```
cmd/            replayer, featurize, accuracy, scorer, graphsvc, casesvc, summarizer, casesapi, drift
internal/       profile/ (+ payments, paysim, registry), features/, graph/, rules/, scoring/,
                model/ (lgbm evaluator + TreeSHAP), align/, replay/, store/, cases/, llm/, api/, dlq/, table/, csvx/
proto/          buf module (input and scored schemas); gen/ holds the generated Go
api/            cases-api OpenAPI spec
web/            Next.js console and Playwright tests
train/          Python CLI (uv): prepare, train, transfer, report, fixture
deploy/         docker/, helm/unfraud (chart, values, Grafana dashboard), kind/
scripts/        fetch-data, system-run, summary-report, console-local
results/        published results (embedded in cases-api)
docs/           design.md, adr/
```

## 13. Milestones

All eight are done; see `git log` for what each delivered.

1. **Data foundations:** Profiles, Protobuf schema, replayer, kind + Strimzi, checksummed `make data`.
2. **Features:** feature engine, Accuracy Run replay to Parquet, first IEEE-CIS model, parity tests in CI.
3. **Scoring:** scorer with rules, model, TreeSHAP and Decisions; first three rows of the results table.
4. **Graph:** graph-svc, Revisions, Rings; remaining results rows.
5. **Cases:** case-svc, Postgres, summarizer with OpenRouter and the stub driver, Grounding metrics.
6. **Web app:** cases-api plus the Next.js app (cases and rings first, then overview, models, benchmarks, replay).
7. **Hardening:** dead-letter topics, degraded mode, Shadow Models, Prometheus and Grafana, System Runs and paced latency, Playwright end-to-end tests. (KEDA and a standby graph-svc were not built; see §11.)
8. **Named datasets:** IEEE-CIS and Elliptic replaced by TabFormer, Sparkov and AMLworld in one Payment Schema; Core Models trained per dataset, pooled, and scored across datasets (ADR-0018).

## 14. Open questions

- Whether a Ring policy beats the model at equal volume on AMLworld, where laundering is built from fan-in, fan-out and cycle patterns.
- AMLworld's larger releases (HI-Medium, 30M transfers) would give a longer test period, but do not fit the reference machine's memory with today's in-memory feature state.
