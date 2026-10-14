# unfraud

A real-time fraud detection engine that runs on a laptop. Card payments stream through Kafka into a Go scorer that combines real-time features, fast rules and a LightGBM model; a single-writer graph service links cards that use the same merchants and revises decisions with graph features; flagged payments become cases with TreeSHAP explanations and grounded LLM summaries; a Next.js console shows it all. Everything runs on local Kubernetes, and every number below is regenerated from a public dataset by the commands in this repository.

The data is [IBM TabFormer](https://github.com/IBM/TabFormer): 9 million simulated card payments by 2,000 cardholders over five years, with every column named ([ADR-0018](docs/adr/0018-named-payment-datasets.md), [ADR-0019](docs/adr/0019-tabformer-only.md)).

- Design: [`docs/design.md`](docs/design.md) · decisions: [`docs/adr/`](docs/adr) · vocabulary: [`CONTEXT.md`](CONTEXT.md)
- Results: [`results/tabformer-lightgbm.md`](results/tabformer-lightgbm.md), [`results/summaries-stub.md`](results/summaries-stub.md)

![A blocked case: grounded summary, decisions from both stages, and the risk ledger from base rate to final probability](docs/screenshots/case.png)

More: [overview](docs/screenshots/overview.png) · [case queue](docs/screenshots/cases.png) · [graph explorer](docs/screenshots/graph.png) · [benchmarks](docs/screenshots/benchmarks.png)

## Results

Time split: 2015–2017 trains, 2018 validates, 2019 tests (1.72M payments, 0.12% fraud). Thresholds are set on 2018 and applied unchanged to 2019.

| Model | PR-AUC | Fraud caught when flagging 0.5% of payments | Precision there |
|---|---|---|---|
| The payment's own fields (amount, merchant code, location, card, cardholder) | 0.457 | 51.5% | 13.7% |
| + 24 real-time features (card and merchant velocity, unusual amounts, new merchants, past fraud) | **0.763** | **90.9%** | 23.5% |
| + 5 graph features (cards linked through shared small merchants) | 0.346 | 70.1% | 8.5% |

- Real-time features are what make it work: a payment looks very different once you know what the card, its cardholder and the merchant did in the hours and weeks before it.
- Graph features made it worse, on validation and on test. Component sizes grow with the simulated population, and the model learns thresholds that stop holding. The scorer serves the model without them; graph-svc still runs, revising decisions and powering the graph explorer.
- Hand-written rules rarely earn their place. A rule stays only if it is at least as precise on validation as the model's own top 2% of alerts; one of six survives ("this merchant recently took confirmed fraud", 15–18% precision).
- Go scores equal Python LightGBM exactly on all 1.72M test payments, and Go TreeSHAP matches LightGBM's to 1e-9.

**The running system** (4-core, 8 GB laptop, `lite` profile):

- The first 1.5M payments replayed into Kafka and scored in the cluster by one scorer pod at 500 a second from a backlog, with features in Valkey. 93.9% of probabilities are bit-identical to the offline Accuracy Run (the rest differ by at most 0.13, 1.4e-9 at p99), and 147 of 1,500,000 decisions differ: partitions run in parallel against shared feature state.
- graph-svc revises every payment with graph features from one ordered stream; case-svc keeps the stricter decision per case. 78,163 cases, every one explained (TreeSHAP) and summarized, with no case-invariant violations (`make check-cases`).

## Run it

Requirements: Linux or macOS with Docker and about 5 GB of free RAM; Go 1.27, buf, kind, kubectl, Helm 4, Node 24 with pnpm, uv; a Kaggle API token in `~/.kaggle/`.

```sh
make data                              # download TabFormer (~2.4 GB), prepare events.csv, verify checksums
make cluster-up deploy images          # kind + Strimzi + CloudNativePG; Kafka, Valkey, Postgres, console

# Train (outside the cluster)
make featurize PROFILE=tabformer       # Accuracy Run -> training tables (~20 min)
make train PROFILE=tabformer           # raw, real-time and graph models (~75 min)
make parity PROFILE=tabformer          # Go scores must equal Python's
make accuracy PROFILE=tabformer MODEL=lgbm-realtime  # the scorer's decisions on 2018-2019, offline
make report PROFILE=tabformer          # results/tabformer-lightgbm.md

# Serve
make deploy-model PROFILE=tabformer MODEL=lgbm-realtime             # the scorer
make deploy-model PROFILE=tabformer MODEL=lgbm-graph SERVICE=graph  # graph-svc
make replay PROFILE=tabformer LIMIT=1500000                         # stream the first 1.5M payments
make accuracy PROFILE=tabformer MODEL=lgbm-realtime LIMIT=1500000  # the same payments offline...
make system-run PROFILE=tabformer                                  # ...rescored live and compared

make console                           # http://localhost:3000
make grafana                           # http://localhost:3001
make tailscale                         # or serve both on this machine's Tailscale IP, across reboots
```

Summaries use a template writer unless you add an OpenRouter key: put it in `~/.config/unfraud/openrouter_key`, run `make secrets`, and deploy with `--set summarizer.driver=openrouter`. `scripts/summary-report.sh` then reports grounding rate, latency and cost per model. `make help` lists every target.

## What it does not do

- The data is simulated. The metrics say how well the engine finds TabFormer's fraud, not how it would do on a real bank's traffic, and the simulator makes some of it easy (73% of transactions in Italy are fraud).
- The live demo replays the first 1.5M of 9M payments. All of them would take most of a day on the reference machine, mostly case-svc writing 18M decisions to Postgres; the published metrics come from the offline Accuracy Run over all of them.
- It is local-only: no authentication, one Kafka broker, one replica per service. KEDA autoscaling, a hot-standby graph service and tracing are designed but not built, because they could not be validated on the reference machine.
- Real-LLM summary quality has not been measured yet (no key was available while building); the grounding check and the reporting are in place.
