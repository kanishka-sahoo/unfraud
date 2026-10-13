# unfraud

A real-time fraud detection engine that runs on a laptop. Transactions stream through Kafka into a Go scorer that combines real-time features, fast rules and a LightGBM model; a single-writer graph service links accounts that share devices (or follows Bitcoin payment flows) and revises decisions with graph features; flagged events become cases with TreeSHAP explanations and grounded LLM summaries; a Next.js console shows it all. Everything runs on local Kubernetes, and every number below is regenerated from public datasets by the commands in this repository.

- Design: [`docs/design.md`](docs/design.md) · decisions: [`docs/adr/`](docs/adr) · vocabulary: [`CONTEXT.md`](CONTEXT.md)
- Results: [`results/ieee-cis-lightgbm.md`](results/ieee-cis-lightgbm.md), [`results/elliptic-lightgbm.md`](results/elliptic-lightgbm.md), [`results/summaries-stub.md`](results/summaries-stub.md)

![A blocked IEEE-CIS case: grounded summary, decisions from both stages, and the risk ledger from base rate to final probability](docs/screenshots/case.png)

More: [overview](docs/screenshots/overview.png) · [case queue](docs/screenshots/cases.png) · [graph explorer](docs/screenshots/graph.png) · [benchmarks](docs/screenshots/benchmarks.png)

## Results

**IEEE-CIS card payments** (time split; test month May 2018, 89,326 transactions, 3.49% fraud):

| Model | PR-AUC | Recall at 1% / 2% / 5% of transactions flagged |
|---|---|---|
| Static features | 0.558 | 24.9% / 42.1% / 62.8% |
| + 30 real-time features | 0.611 | 27.6% / 45.6% / 65.8% |
| + 5 device-graph features | **0.625** | **27.9% / 46.1% / 66.4%** |

- Hand-written rules did not beat the model: at the same alert volume the model alone catches more fraud (59.7% vs 50.1% recall). Three rules were retired on validation-month evidence.
- Ring alerts (flagging every card in a fraud-heavy device community) lost to the model at equal volume (5.1% vs 18.9% precision on the validation month) and are off; the graph helps through its features.

**Elliptic Bitcoin** (test timesteps 35–49, the period Weber et al. 2019 used):

| | Illicit F1 | PR-AUC |
|---|---|---|
| Provisional: transaction's own features, on arrival | 0.734 | 0.766 |
| Revised: + payment-graph features, when its timestep closes | **0.780** | **0.793** |
| Weber et al.: Random Forest / GCN | 0.788 / 0.628 | |

The stream's own graph features match the dataset's precomputed neighbour aggregates (PR-AUC 0.793 vs 0.792). Every model collapses from timestep 43 (a dark-market shutdown); the report shows it.

**The running system** (4-core, 8 GB laptop, full pipeline in the cluster at about 3.7 GB):

- Go scores equal Python LightGBM exactly, and Go TreeSHAP matches LightGBM's to 1e-9.
- Live features in Valkey are bit-identical to the offline training features, including when every record is delivered twice.
- Revised decisions from graph-svc match the offline Accuracy Run on all 590,540 IEEE-CIS and 203,769 Elliptic events; provisional decisions differ on 77 of 590,540 (partition concurrency).
- End-to-end latency, Kafka in to decision out, at about 200 events/s: provisional p50 17 ms, p99 428 ms; revised p50 7 ms, p99 65 ms.

## Run it

Requirements: Linux or macOS with Docker and about 5 GB of free RAM; Go 1.27, buf, kind, kubectl, Helm 4, Node 24 with pnpm, uv; a Kaggle API token in `~/.kaggle/` (for IEEE-CIS, accept the [competition rules](https://www.kaggle.com/competitions/ieee-fraud-detection/rules) with a phone-verified account).

```sh
make data                              # download datasets (~2.5 GB), verify checksums
make cluster-up deploy images          # kind + Strimzi + CloudNativePG; Kafka, Valkey, Postgres, console

# Train (outside the cluster)
make featurize PROFILE=ieee-cis        # Accuracy Run -> training tables
make train PROFILE=ieee-cis            # static, real-time and graph models
make parity PROFILE=ieee-cis           # Go scores must equal Python's

# Serve
make deploy-model PROFILE=ieee-cis MODEL=lgbm-realtime             # the scorer
make deploy-model PROFILE=ieee-cis MODEL=lgbm-graph SERVICE=graph  # graph-svc
make replay PROFILE=ieee-cis                                        # stream the dataset

make console                           # http://localhost:3000
make grafana                           # http://localhost:3001
make tailscale                         # or serve both on this machine's Tailscale IP, across reboots
```

Summaries use a template writer unless you add an OpenRouter key: put it in `~/.config/unfraud/openrouter_key`, run `make secrets`, and deploy with `--set summarizer.driver=openrouter`. `scripts/summary-report.sh` then reports grounding rate, latency and cost per model. `make help` lists every target.

## What it does not do

- It is local-only: no authentication, one Kafka broker, one replica per service. KEDA autoscaling, a hot-standby graph service and tracing are designed but not built, because they could not be validated on the reference machine.
- IEEE-CIS has no raw IP addresses or coordinates, so "movement" means address and distance-field changes, and only 26% of transactions carry device data for the graph.
- The card identity is the Kaggle community's reconstruction (card1, address and first-seen day). When the address is missing it collapses unrelated cards into one identity; some of those form hundreds-of-node hubs in the graph explorer.
- PaySim is used for load testing only; its balance columns give away the label.
- Real-LLM summary quality has not been measured yet (no key was available while building); the grounding check and the reporting are in place.
