# unfraud

A real-time fraud detection engine that runs on a laptop. Card payments stream through Kafka into a Go scorer that combines real-time features, fast rules and a LightGBM model; a single-writer graph service links cards that use the same merchants and revises decisions with graph features; flagged payments become cases with TreeSHAP explanations and grounded LLM summaries; a Next.js console shows it all. Everything runs on local Kubernetes, and every number below is regenerated from a public dataset by the commands in this repository.

The data is [IBM TabFormer](https://github.com/IBM/TabFormer): 9 million simulated card payments by 2,000 cardholders over five years, with every column named ([ADR-0018](docs/adr/0018-named-payment-datasets.md), [ADR-0019](docs/adr/0019-tabformer-only.md)).

- Design: [`docs/design.md`](docs/design.md) · decisions: [`docs/adr/`](docs/adr) · vocabulary: [`CONTEXT.md`](CONTEXT.md)
- Results: [`results/tabformer-lightgbm.md`](results/tabformer-lightgbm.md), [`results/summaries-stub.md`](results/summaries-stub.md)

![A blocked case: grounded summary, decisions from both stages, and the risk ledger from base rate to final probability](docs/screenshots/case.png)

More: [overview](docs/screenshots/overview.png) · [case queue](docs/screenshots/cases.png) · [graph explorer](docs/screenshots/graph.png) · [benchmarks](docs/screenshots/benchmarks.png)

## Results

Time split: January 2015 to September 2018 trains, October–December 2018 validates, 2019 tests (1.72M payments, 0.12% fraud). Thresholds and probability calibration are set on validation and applied unchanged to 2019.

| Model | PR-AUC | Fraud caught when flagging 0.1% of payments | Precision there | Fraud caught when flagging 0.5% |
|---|---|---|---|---|
| The payment's own fields (amount, merchant code, location, card, cardholder) | 0.703 | 47.2% | 95.1% | 75.8% |
| + 24 real-time features (card and merchant velocity, unusual amounts, new merchants, past fraud) | **0.958** | 43.2% | 98.8% | **100%** |
| + 6 graph features (fraud among the cards sharing this card's merchants) | 0.939 | 59.2% | 93.6% | 100% |

- Real-time features are what make it work: a payment looks very different once you know what the card, its cardholder and the merchant did in the hours and weeks before it.
- Training on recent data matters as much. Fraud before 2018 looks different (2016 had a wave, 2017 almost none), and the same model trained only on 2015–2017 reached 0.763 ([ADR-0020](docs/adr/0020-calibration-recent-training-and-neighbourhood-graph.md)).
- Graph features do not beat real-time features on validation (0.938 against 0.949), so the scorer serves the model without them, and graph-svc revises with them. They describe the card's neighbourhood: how many cards that used the same merchants later had confirmed fraud. Connected-component sizes made the model worse (0.346), because one component holds most cards and its size only tracks the population.
- Probabilities are calibrated (Platt scaling fitted on validation). On 2019, payments scored above 50% were fraud 89% of the time (mean score 92%), and those scored 20–50% were fraud 44% of the time (mean 35%). Scores from 1% to 10% overstate risk: on 2019 they were fraud 0.1–2% of the time.
- Hand-written rules rarely earn their place. A rule stays only if it is at least as precise on validation as the model's own top 2% of alerts; one of six survives ("this merchant recently took confirmed fraud", 14% precision on validation).
- Go scores equal Python LightGBM exactly on all 1.72M test payments, and Go TreeSHAP matches LightGBM's to 1e-9.

**The running system** (4-core, 8 GB laptop, `lite` profile):

- The first 1.5M payments replayed into Kafka and scored in the cluster by one scorer pod at 468 a second from a backlog, with features in Valkey. 93.8% of probabilities are bit-identical to the offline Accuracy Run (the rest differ by at most 0.23, 2.1e-6 at p99), and 116 of 1,500,000 decisions differ: partitions run in parallel against shared feature state.
- graph-svc revises every payment with graph features from one ordered stream; case-svc keeps the stricter decision per case. 49,493 cases, every one explained (TreeSHAP) and summarized, with no case-invariant violations (`make check-cases`).

## Run it

Requirements: Linux or macOS with Docker and about 5 GB of free RAM; Go 1.27, buf, kind, kubectl, Helm 4, Node 24 with pnpm, uv; a Kaggle API token in `~/.kaggle/`.

```sh
make data                              # download TabFormer (~2.4 GB), prepare events.csv, verify checksums
make cluster-up deploy images          # kind + Strimzi + CloudNativePG; Kafka, Valkey, Postgres, console

# Train (outside the cluster)
make featurize PROFILE=tabformer       # Accuracy Run -> training tables (~20 min)
make train PROFILE=tabformer           # raw, real-time and graph models (~75 min)
make parity PROFILE=tabformer          # Go scores must equal Python's
make accuracy PROFILE=tabformer MODEL=lgbm-realtime  # the scorer's decisions on late 2018 and 2019, offline
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

- 2019 was looked at while choosing the training window, so it is not an untouched holdout for that choice ([ADR-0020](docs/adr/0020-calibration-recent-training-and-neighbourhood-graph.md)); the choice rests on validation.
- The data is simulated. The metrics say how well the engine finds TabFormer's fraud, not how it would do on a real bank's traffic, and the simulator makes some of it easy (73% of transactions in Italy are fraud).
- The live demo replays the first 1.5M of 9M payments. All of them would take most of a day on the reference machine, mostly case-svc writing 18M decisions to Postgres; the published metrics come from the offline Accuracy Run over all of them.
- It is local-only: no authentication, one Kafka broker, one replica per service. KEDA autoscaling, a hot-standby graph service and tracing are designed but not built, because they could not be validated on the reference machine.
- Real-LLM summary quality has not been measured yet (no key was available while building); the grounding check and the reporting are in place.
