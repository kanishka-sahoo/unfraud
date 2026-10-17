# unfraud: design

Status: draft, 2026-10-12. Terms in **bold** are defined in [`CONTEXT.md`](../CONTEXT.md); decisions link to [`adr/`](./adr).

## 1. Goals

- Score each **Event** within tens of milliseconds, using fast rules plus a gradient-boosted model over features that update in real time.
- Catch coordinated fraud that looks normal one Event at a time by linking **Entities** into a graph and raising **Ring Alerts**.
- Give an analyst a **Case** for every flag, including a grounded LLM **Summary**, in a multi-page web app.
- Run like a production system (horizontal scaling, observability, defined degraded modes), deployed to local Kubernetes.
- Publish every metric with a single command that anyone can rerun from public data.

**Non-goals:** a cloud deployment; training on analyst **Dispositions**; claiming accuracy on data we can't evaluate honestly (see §2).

## 2. Dataset ([ADR-0018](./adr/0018-named-payment-datasets.md), [ADR-0019](./adr/0019-tabformer-only.md))

One dataset, IBM TabFormer: 2,000 synthetic US cardholders with several cards each, October 2014 to December 2019 (9.0M Events, 0.13% fraud). Every column has a published meaning, which is why it replaced IEEE-CIS and Elliptic, whose features were mostly masked; it exercises every part of the pipeline, which is why it is the only one (ADR-0019). It is a simulation, so we claim "reproducible from public, synthetic data": metrics say how well the engine finds the simulator's fraud, not how it would do on a real bank's.

`unfraud-train prepare` (`train/src/unfraud_train/prepare.py`) joins the transactions with the card and cardholder files and writes `data/raw/tabformer/events.csv`, sorted by Event Time (the raw file is sorted by card):

| Column | From |
|---|---|
| `payer` (**Payer**) | user + card index |
| `payee` (**Payee**) | merchant |
| `customer` (**Customer**) | user |
| `amount_usd`, `channel` | amount; `online` or `in_person` |
| `category` | MCC grouped into 11 categories |
| extras | entry mode (chip, swipe, online), MCC, authorization errors, merchant state, abroad, out of home state, card brand, type, chip and credit limit, cardholder age, income and FICO score |

`make data` downloads with the user's own Kaggle credentials, prepares, and checks SHA-256 hashes of both the raw and the prepared files against `data/checksums.sha256`. TabFormer is released by IBM under Apache-2.0 (the Kaggle mirror says "other"). The data is never committed.

**Event Time** is the transaction's date and minute, read as UTC. Only October 2014 to December 2019 is kept: earlier years have few cardholders, and 2020 has no fraud labels yet.

**Split** by Event Time (`profile.Split`): October–December 2014 only warms up the real-time features; January 2015 to September 2018 trains, October–December 2018 validates, 2019 tests. Fraud before 2018 looks different, and a model trained only on 2015–2017 missed most of 2019's ([ADR-0020](./adr/0020-calibration-recent-training-and-neighbourhood-graph.md)).

Limitations we state openly:

- TabFormer fraud comes in bursts: 2017 has 255 fraud Events, 2016 has 3,579.
- The simulator makes some fraud easy: 73% of transactions in Italy are fraud, and the MCC alone carries most of a raw-feature model's signal.
- Online transactions have no merchant location.

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

- Events and Label Releases are keyed by the primary Entity (the Payer). Each Watermark is copied to every partition.
- Event features are positional: `repeated double num` (NaN = missing) and `repeated string cat` ("" = missing), in the order given by the Profile's **Feature Schema**. A `schema_id` hash lets consumers reject Events built against a different schema.
- Label Releases come from a second cursor over the dataset that trails the first by the label delay, so the replayer's memory stays constant.

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
                         assistant ──► Claude API   (Summaries on request; chat over read-only lookups)
                                    │
   cases-api (Go, REST + SSE) ◄── Postgres, Kafka tail, Prometheus, assistant
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
| `assistant` | Go | One replica, rate-limited | Writes a **Summary** when an analyst asks for one (Case Packet, Claude Haiku, Grounding check, one retry), and answers **Investigations**, the console's assistant chat, with Claude Sonnet over read-only lookups ([ADR-0009](./adr/0009-llm-via-openrouter-off-hot-path.md), [ADR-0022](./adr/0022-summaries-on-request-and-investigations.md)). |
| `cases-api` | Go | Horizontal | REST (OpenAPI) + SSE for the web app. Analyst Dispositions. Replay control (creates replayer Jobs). Model promotion. Forwards Investigations to the assistant. |
| `web` | Next.js | Horizontal | The analyst and operator UI (§9). |
| `train` | Python CLI | Batch Job | Turns the feature Parquet into LightGBM models and evaluation reports. Not part of the runtime. |

### Delivery semantics

Producers are idempotent; consumers process at-least-once. Every Event's and Label Release's feature updates carry a Guard (`e|<event_id>`, `l|<event_id>`) that Valkey checks with `SET NX` inside the same Lua script, so a redelivered record never counts twice (it is still scored, against state that already includes it). Guards expire 7 days later in Event Time. Consumers of `scored.<profile>` must deduplicate on `event_id`. Schemas are buf-managed Protobuf compiled into every service, with no runtime schema registry; `buf breaking` in CI is the compatibility gate ([ADR-0014](./adr/0014-no-schema-registry.md)).

## 5. Features ([ADR-0004](./adr/0004-event-time-and-label-release.md), [ADR-0005](./adr/0005-single-feature-implementation.md))

All windows are in **Event Time**. Each feature is declared on its Profile as a `profile.Feature` value (Go, type-checked; no YAML) and computed by one Go engine (`internal/features`), which serves both training and live scoring. Features always describe the Entity's history **before** the Event being scored.

Windowed counts and sums are bucketed: a window of length W is read as the 24 buckets of W/24 ending with the current one, so it reaches back between 23/24·W and W. Distinct counts are exact sliding windows (last-seen time per member). Engine state goes through a batched `Get`/`Apply` interface, so the Valkey implementation needs one read and one write round trip per Event.

TabFormer's real-time features (`internal/profile/tabformer/features.go`):

- **Velocity:** count and amount per Payer over 1h/24h/7d; distinct Payees per Payer (24h/7d); count per Payee (1h/24h); distinct Payers per Payee (24h/7d); distinct Payers per Customer (7d).
- **Unusual behaviour:** amount ÷ the Payer's 30-day mean and ÷ the Payee's 7-day mean; seconds since the Payer's and the Payee's previous Event; first payment from this Payer to this Payee; channel or category different from the Payer's previous Event.
- **Released Labels:** fraud Labels on the Payer (30d/90d), the Payee (30d) and the Customer (90d).
- **Graph** (§7): released fraud among the cards that used the Payee, among the cards sharing the Payer's small merchants, and in the Payer's Community.

**Label Release** rule: nothing may read a Label before `event_time + delay`. The delay is 30 days, configurable, and recorded in the results.

State lives in Valkey as time-bucketed counters, with Lua scripts that update and read atomically, pipelined per Event. If Valkey is unavailable, the scorer falls back to static features and marks the Decision degraded.

## 6. Scoring ([ADR-0003](./adr/0003-two-stage-scoring.md), [ADR-0008](./adr/0008-pure-go-runtime-inference.md))

1. **Rules** (`profile.Rule` values): all conditions hold → review or block, with a Reason Code. Rules only escalate the model's Decision. They are written before results and reviewed on the validation period only: a Rule less precise there than the model's top 2% (the review budget) is retired, kept in code with the evidence. A Rule only escalates, so a less precise one only adds alerts the model would have ranked lower.
2. **Model**: LightGBM served in-process by our own Go evaluator (`internal/model/lgbm`; `leaves` can't read LightGBM 4.x models). CatBoost was planned as an offline comparison but has not been trained or evaluated.
3. **Contributions**: Go TreeSHAP, checked in CI against LightGBM `pred_contrib` (tolerance 1e-9), alongside the Go-vs-Python score parity tests (fixtures in CI, real data with `make parity`). TreeSHAP costs about trees × leaves × depth², so it runs in case-svc for flagged Events only, not in the scoring path. Models are trained with `max_depth=10`, which kept scoring at about 21 µs and TreeSHAP at about 16 ms per Event on the earlier IEEE-CIS models.
4. **Decision**: the raw score is calibrated into a probability by Platt scaling fitted on the validation period (stored in the model manifest; linear in log-odds, so rankings and Contributions' additivity are kept). Thresholds are set from the **Alert Budget** on the validation period.
5. **Revision**: graph-svc re-scores Events with the graph model, and Ring Alerts (when enabled) revise recently approved Events. Revised Decisions update the Case; the Provisional Decision is never overwritten.

Latency budget (targets, to be measured): consume → Decision under 10 ms p99 inside the scorer, under 50 ms p99 from end to end.

## 7. Graph (rings)

- **Attribute graph** (`internal/graph`): cards link through merchants they both paid. A merchant used by more than 100 cards within the 30-day window is a **Super-node** and never links. The cap was chosen on the training years only: merchants used by 21–100 cards carry 3.2× the base fraud rate, more popular ones 0.8×.
- **Features describe the neighbourhood, not the Component** ([ADR-0020](./adr/0020-calibration-recent-training-and-neighbourhood-graph.md)). Chains of shared merchants put about 3,800 cards into one Component every day, so its size and fraud count only tracked the population and the overall fraud level; with them the graph model fell to 0.346 test PR-AUC. The six features are: other cards that used the Payee in the window with released fraud (count and share); cards sharing any of the Payer's small merchants with released fraud (count and share, counted once per shared merchant); and the Payer's Louvain Community (size, and share of its other cards with released fraud). The five fraud features are trained with monotone constraints, so more fraud nearby can only raise risk.
- **They still do not beat the real-time features.** On validation the graph model reaches 0.938 PR-AUC against the real-time model's 0.949 (test 0.939 against 0.958). The scorer serves the real-time model; graph-svc revises with the graph model, so the explorer and Revisions run, and the Case keeps the stricter Decision.
- The graph is rebuilt daily in Event Time (union-find cannot delete edges) and only grows in between. Each rebuild runs a deterministic Louvain and recounts released fraud per merchant; Label Releases update those counts as they arrive.
- **Rings and Ring Alerts**: a Community with at least 3 Payers, 2 of them with released fraud, flags its Payers' later Events and revises their Events approved in the previous 7 days. Off unless a Profile enables it after beating the model at equal alert volume on validation.
- **No GNN in serving** ([ADR-0012](./adr/0012-no-gnn-in-serving.md)).

## 8. Cases, Summaries and the assistant ([ADR-0009](./adr/0009-llm-via-openrouter-off-hot-path.md), [ADR-0022](./adr/0022-summaries-on-request-and-investigations.md))

- **case-svc** (one per Profile) consumes `scored.<profile>` and `revised.<profile>`, stores every Decision (`decisions` table, redeliveries ignored), and opens a **Case** for each flagged Event. A Case keeps the stricter of its Provisional and Revised Decisions and is never made less strict. When a Case opens or becomes stricter, case-svc computes **Contributions** (Go TreeSHAP) with the model that flagged it, matched by model version; it holds both the scorer's and graph-svc's models.
- The **Case Packet** (`internal/cases`) holds the Decision, rule Reason Codes with descriptions, Entities, and the strongest model factors with plain-language descriptions generated from each feature's declaration. Every raw feature has a published meaning in the Profile's Glossary, so every factor is described. (A feature without one would be combined into a single "anonymized features" effect rather than guessed at.)
- The packet also carries what an analyst would look up next (`internal/investigate`): the card's alerts and their outcomes over 30 days, the merchant's alerts on any card over 7 days, other cards' alerts at the merchant within 24 hours, the card's network from graph-svc, and both Stages' Decisions.
- Summaries are written **only when an analyst asks** (ADR-0022): at 1,000+ Events a second, summarizing every blocked Case on arrival spent nearly everything on Cases nobody opens. The **assistant** claims requested Cases from a Postgres queue (`FOR UPDATE SKIP LOCKED`, oldest request first, optional rate limit) and survives restarts (stale claims are requeued). A Summary has four parts: what happened and the pattern, supporting facts, wider context, and what to check next; it never suggests a Disposition.
- **Drivers** ([ADR-0021](./adr/0021-claude-direct-for-summaries.md)): `anthropic` (the default: Claude Haiku 5.5 through the Messages API, or any Anthropic-compatible API; structured output, `effort: low`), `openai` (any OpenAI-compatible API, `json_schema` output), `openrouter` (the `openai` driver with OpenRouter's cost reporting and optional provider pinning with fallbacks off), and `stub` (deterministic template, no key, grounded by construction). Every Summary stores the model, provider, tokens, cost and latency.
- **Grounding** (`cases.Ground`): every number in a Summary must be a rounding of a Packet value or a percentage of one (whole numbers up to 10 are allowed for counting words), and every identifier-like token must appear verbatim. An ungrounded Summary is retried once, then stored as `failed` and never shown.
- **Investigations** (`internal/agent`) are the console's assistant chat. Claude Haiku 5.5 by default (adaptive thinking, `effort: medium`; Sonnet 5.5 also gets a server-side refusal fallback) answers over several steps with six read-only lookups: `get_alert`, `search_alerts`, `card_history`, `merchant_activity`, `card_network`, `related_alerts`, at most 8 per question. Each lookup returns facts for the model and a sentence and table for the console. Messages are stored as the Messages API's own JSON in a `text` column (jsonb would reorder keys, and thinking blocks must go back byte for byte); a rewind (edit and resend, retry) marks later messages superseded rather than deleting them, so the history stays append-only for the API and complete for audit. Every number and ID in an answer is checked against everything looked up (`cases.GroundText`); what fails is marked "not verified" in the console, never hidden. A token budget per Investigation and a limit on questions answered at once bound spend; without `ANTHROPIC_API_KEY` a scripted stub answers.
- **Published** by `scripts/assistant-report.sh`: questions by outcome, answers with unverified details, lookups per question, tokens and cost per question per model, plus a sample for human grading.
- **Published** by `scripts/summary-report.sh`: Grounding pass rate, retries, errors, p50/p99 latency and cost per 1k Cases per model, plus a random sample for human grading.
- The LLM never affects a Decision and is never in the scoring path.

## 9. Web app (Next.js)

Next.js 16 (App Router, Cache Components: every page ships a static shell and streams its data behind `<Suspense>`) with TypeScript. Server components call `cases-api` inside the cluster; browser calls (live stream, dispositions, replays, graph) go through one route-handler proxy, so the browser talks to a single origin. API types are generated from `api/openapi.yaml` with openapi-typescript, and CI fails if they drift. There is no auth: the console is local-only.

Visual direction: intaglio-ink navy with paper-coloured text; decisions are always an icon plus a word; the analyst memo (the LLM Summary) is set in a serif because it is prose; chart colours were validated for colour-vision deficiency against both navy surfaces.

| Route | Purpose |
|---|---|
| `/` | **Dashboard**: payments scored, flag rates per Stage, open alerts, Summaries, Decisions per minute (zero-filled last hour), highest-risk open alerts. |
| `/alerts` | **Alert queue**: status, strictness and order filters in the URL; entity filter. |
| `/alerts/{id}` | **Alert detail**: the Summary (written on request) with its Grounding note and model, latency and cost; why it was flagged and the full score breakdown; payment details; Decisions from both Stages; Entities (each a link to that Entity's alerts); Disposition. |
| `/live` | **Live Decisions** over server-sent events (at most 20 a second per browser, with a count of the rest). |
| `/network`, `/network/{card}` | **Network**: a card's merchants and the cards that share them, from graph-svc's live state; released fraud marked. |
| `/models` | Model versions in use per Profile and Stage, from the Decisions they made. |
| `/benchmarks` | The committed `results/` (embedded in cases-api). |
| `/replay` | Start a replay Job (cases-api creates it through the Kubernetes API with a namespaced Role) and see recent ones. |

The **assistant** is a sidebar on every page, not a route. A conversation opens on what is on screen (an alert, a card, or the whole console), and each alert or card keeps its own conversation in the tab. It streams each lookup as a plain sentence ("Looking at card 77-1's alerts over 30 days"), folds them into "How I worked this out" with the data as tables once the answer arrives, links alert and card IDs in answers, and marks numbers it could not verify. Analysts can stop an answer, edit and resend any earlier question, retry, start a new chat, and reopen earlier conversations. It is written for fraud analysts rather than developers: no tool names, JSON or model internals.

**Dispositions** are stored but never become Labels and never feed metrics or training ([ADR-0011](./adr/0011-dispositions-are-not-labels.md)). Playwright tests (`web/e2e`) run against the deployed console.

## 10. Benchmarking ([ADR-0006](./adr/0006-accuracy-vs-system-runs.md))

- **Accuracy Run**: deterministic Replay that pauses at each Watermark until graph-svc catches up, so features are exact as of Event Time. All fraud metrics come from this mode, and so does the training Parquet.
- **System Run**: production topology, N scorers, full speed. Reports throughput, p50/p99 latency, consumer lag, and score drift compared with the Accuracy Run (`cmd/drift`: the measured cost of concurrency). Scorer partitions are aligned in Event Time ([ADR-0016](./adr/0016-event-time-alignment-across-partitions.md)); without that, a backlog let partitions drift apart and 96% of scores differed.

`make train report`: PR-AUC and recall at Alert Budgets of 0.1%, 0.5%, 1% and 2% of Events, with dollar-weighted recall and precision at 0.5%, for the raw features, + real-time, and + graph. Each budget's threshold is set on the validation period and applied unchanged to the test period, and each table prints the recall a perfect model could reach at each budget.

`make bench` runs everything and writes `results/<run-id>.json`, including dataset hashes, git SHA, hardware, Label Release delays and LLM provider pins.

## 11. Deployment: local Kubernetes only ([ADR-0010](./adr/0010-local-kubernetes-only.md))

What is built and runs on the 4-core, 8 GB reference machine (`lite`, the default values):

- **kind** single-node cluster (`make cluster-up`), images loaded with `kind load` rather than a registry, one Helm chart (`make deploy`, `make deploy-model`).
- **Operators:** Strimzi (Kafka 4.3 in KRaft mode, one broker with a 512 MB heap) and CloudNativePG (one Postgres instance). Valkey is a plain StatefulSet with append-only persistence and `noeviction`; its expiry is in Event Time, never wall-clock TTLs.
- **Services:** scorer and graph-svc per Profile (one replica each), case-svc per Profile, one assistant, cases-api, and the web console (NodePort 30080 on new clusters, `make console` on older ones).
- **Failure handling:** idempotent producers, at-least-once consumers with idempotency Guards on feature state; records that cannot be processed go to `dlq.<profile>` with the error and origin in headers; when Valkey is unreachable the scorer keeps deciding with real-time features missing and marks Decisions `degraded`; graph-svc recovers by re-reading its input. An optional **Shadow Model** scores every Event beside the active one (recorded, never acted on).
- **Observability:** Prometheus (pods opt in with `prometheus.io/scrape`, 12-hour retention) and Grafana with a provisioned dashboard (`make grafana`): throughput and latency per Stage, degraded and dead-lettered records, Shadow agreement, graph-svc progress, case-svc, Summaries and the assistant chat. Kafka and Valkey internals are not scraped.
- Every pod has a memory limit. With a full pipeline running, the cluster uses about 3.7 GB.
- A full live replay of TabFormer (9M Events) would take most of a day here, mostly case-svc storing 18M Decisions in Postgres, so the System Run covers the first 1.5M Events (ADR-0019).
- Secrets (the LLM API keys) are Kubernetes Secrets that `make secrets` creates from the git-ignored `.env`. Nothing leaves the machine except calls to the LLM provider.
- Model training runs outside the cluster.

Not built, because none of it could be validated on the reference machine: KEDA autoscaling, a hot-standby graph-svc (graph-svc is a single replica with `Recreate` rollouts and recovers by recomputation instead), OpenTelemetry tracing, and Tilt. `values-full.yaml` raises broker count and partitions but has not been run. Published System Run numbers come from `lite` on the reference machine.

## 12. Repo layout

```
cmd/            replayer, featurize, accuracy, scorer, graphsvc, casesvc, assistant, casesapi, drift
internal/       profile/ (+ tabformer, registry), features/, graph/, rules/, scoring/,
                model/ (lgbm evaluator + TreeSHAP), align/, replay/, store/, cases/, investigate/, llm/, agent/, api/,
                dlq/, table/, csvx/
proto/          buf module (input and scored schemas); gen/ holds the generated Go
api/            cases-api OpenAPI spec
web/            Next.js console and Playwright tests
train/          Python CLI (uv): prepare, train, report, fixture
deploy/         docker/, helm/unfraud (chart, values, Grafana dashboard), kind/
scripts/        fetch-data, system-run, summary-report, assistant-report, console-local
results/        published results (embedded in cases-api)
docs/           design.md, adr/
```

## 13. Milestones

All are done; see `git log` for what each delivered.

1. **Data foundations:** Profiles, Protobuf schema, replayer, kind + Strimzi, checksummed `make data`.
2. **Features:** feature engine, Accuracy Run replay to Parquet, first IEEE-CIS model, parity tests in CI.
3. **Scoring:** scorer with rules, model, TreeSHAP and Decisions; first three rows of the results table.
4. **Graph:** graph-svc, Revisions, Rings; remaining results rows.
5. **Cases:** case-svc, Postgres, summarizer (now the assistant) with OpenRouter and the stub driver, Grounding metrics.
6. **Web app:** cases-api plus the Next.js app (cases and rings first, then overview, models, benchmarks, replay).
7. **Hardening:** dead-letter topics, degraded mode, Shadow Models, Prometheus and Grafana, System Runs and paced latency, Playwright end-to-end tests. (KEDA and a standby graph-svc were not built; see §11.)
8. **Named data:** IEEE-CIS and Elliptic replaced by fully named datasets (ADR-0018), then narrowed to TabFormer alone (ADR-0019).
9. **Calibration and graph features:** calibrated probabilities, neighbourhood graph features and a recent training window (ADR-0020).

From [ADR-0022](./adr/0022-summaries-on-request-and-investigations.md):

10. **Summaries on request:** the summarizer claims only Cases an analyst asked for; new Cases start at `none`.
11. **Context layer and Summary v2:** `internal/investigate` (30-day card history, merchant, network, related Cases, Decisions) feeds a sectioned Summary that passes the Grounding check.
12. **Investigation backend:** the summarizer becomes `assistant`; a multi-step Claude tool loop over read-only tools, SSE through cases-api, `investigations` tables with rewind (superseded messages), stop, retry and token budgets, a stub agent, metrics.
13. **Assistant sidebar:** on every page, opening on what's on screen; steps shown as plain sentences with data tables and links; edit-and-resend, retry, stop, new chat and earlier Investigations; suggested questions; unverified numbers marked; Playwright tests against the stub.
14. **Measurement:** Grounding rate for Summaries and chat answers, tool calls per question, cost per Investigation, in `results/`.

## 14. Open questions

- Graph features that beat the real-time ones: on TabFormer, released fraud among neighbouring cards mostly repeats what the merchant's own fraud Labels already say.
- Thresholds from probabilities instead of Alert Budgets: with calibrated scores, the 0.5% block budget blocks payments with a fraud probability as low as 0.4%.
- A full live replay needs case-svc to store Decisions faster (batched `COPY`, or storing only flagged Events' Decisions).
