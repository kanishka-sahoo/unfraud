# unfraud

A real-time fraud detection engine. It scores transactions as they stream in, links accounts that share devices, emails and cards to catch coordinated fraud rings, and writes an analyst summary for each flagged case. It runs on local Kubernetes, and every metric can be reproduced from public datasets.

- Design: [`docs/design.md`](docs/design.md)
- Decisions: [`docs/adr/`](docs/adr)
- Vocabulary: [`CONTEXT.md`](CONTEXT.md)

**Status:** milestone 3 (scoring). Datasets replay into Kafka; a Go feature engine computes 30 real-time features exactly as of each Event's time (in memory for training, in Valkey live, bit-identical); LightGBM models are trained in Python and served by a Go evaluator whose scores and TreeSHAP values match LightGBM exactly; the scorer runs in the cluster at about 1,300 events/s. Graph, cases and the web app are next.

Current results: [`results/ieee-cis-lightgbm.md`](results/ieee-cis-lightgbm.md).

## Requirements

- Linux or macOS with Docker, and about 5 GB of free RAM for the default `lite` cluster
- Go 1.27, [buf](https://buf.build), kind, kubectl, Helm 4
- A Kaggle account with an API token in `~/.kaggle/`. For IEEE-CIS, accept the [competition rules](https://www.kaggle.com/competitions/ieee-fraud-detection/rules); Kaggle also requires a phone-verified account to download competition data.

## Quickstart

```sh
make data          # download datasets (~2.5 GB) and verify checksums
make cluster-up    # kind cluster + Strimzi operator
make deploy        # Kafka (KRaft) and input topics
make images        # build images and load them into kind
make replay PROFILE=elliptic          # stream Elliptic into input.elliptic
make replay PROFILE=paysim SPEED=3600 # one hour of Event Time per second

# Training and evaluation (outside the cluster)
make featurize PROFILE=ieee-cis       # Accuracy Run -> data/features/ieee-cis
make train PROFILE=ieee-cis           # static and real-time LightGBM models
make parity PROFILE=ieee-cis          # Go scores must equal Python's
make deploy-model PROFILE=ieee-cis    # ship the model as an image, run its scorer
```

`make help` lists every target. `make test` runs unit tests; `make test-integration` also runs them against the cluster's Kafka.

## Datasets

| Profile | Role | Licence |
|---|---|---|
| IEEE-CIS | Fraud metrics (model and attribute graph) | Kaggle competition rules |
| Elliptic | Graph metrics | CC BY-NC-ND 4.0 |
| PaySim | Load testing only | CC BY-SA 4.0 |

See [ADR-0001](docs/adr/0001-dataset-roles.md) for why each dataset has only one role.
