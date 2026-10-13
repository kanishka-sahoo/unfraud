## sparkov: LightGBM, time split

Thresholds for each Alert Budget are set on the validation period and applied unchanged to the test period, so the share of test Events flagged can drift from the budget; the alert-rate column shows by how much. Label delay 720h0m0s.

| Configuration | PR-AUC | ROC-AUC | Recall @ 0.1% | Recall @ 0.5% | Recall @ 1.0% | Recall @ 2.0% | Test alert rate @ 0.5% | $-recall @ 0.5% | Precision @ 0.5% | Trees |
|---|---|---|---|---|---|---|---|---|---|---|
| Model, raw features (core + dataset extras) | 0.7936 | 0.9924 | 17.5% | 70.3% | 85.0% | 91.8% | 0.34% | 83.4% | 80.3% | 60 |
| + real-time features | 0.9395 | 0.9970 | 16.6% | 78.9% | 96.2% | 97.8% | 0.31% | 86.2% | 97.1% | 1000 |
| Core features only (shared by every payment Profile) | 0.9521 | 0.9988 | 18.1% | 82.5% | 97.1% | 98.8% | 0.33% | 87.4% | 96.5% | 514 |

Recall ceilings (fraud a perfect model could catch at each budget): 0.1% → 18%, 0.5% → 88%, 1.0% → 100%, 2.0% → 100%.

Split: train 1,096,840 Events (0.58% fraud), val 229,333 Events (0.56% fraud), test 523,807 Events (0.38% fraud).
Features table sha256 `351b92e5925e45df…`, LightGBM 4.7.0, featurize at `unknown`.

### Decisions (scorer Accuracy Run, served model)

The scorer blocks above the 0.5%-budget threshold (0.6847) and sends to review above the 2%-budget threshold (9.255e-06); Rules can only escalate.

| Decision source | Alert rate | Precision | Recall | $-recall |
|---|---|---|---|---|
| Rules only | 0.00% | 0.0% | 0.0% | 0.0% |
| Model only (review + block) | 1.82% | 20.7% | 97.8% | 99.3% |
| Model + Rules | 1.82% | 20.7% | 97.8% | 99.3% |
| Model only, at the same alert volume as Model + Rules | 1.62% | 23.1% | 97.5% | 99.3% |
| Blocks only (model + rules) | 0.31% | 97.1% | 78.9% | 86.2% |

Every Rule is retired on this Profile: none was as precise on the validation period as the model at the review budget (`internal/profile/payments`).

### System Run (in-cluster scorer, lite profile, 4-core / 8 GB machine)

One scorer pod scoring the whole input topic from a backlog, with features in Valkey, compared Event by Event with the Accuracy Run (`scripts/system-run.sh`, `cmd/drift`). End-to-end latency is not reported here: a backlog measures queueing, not scoring; it needs a live-paced replay.

| Scorer | Events/s | Probabilities differing | Mean abs diff | p99 abs diff | Max abs diff | Model decisions differing |
|---|---|---|---|---|---|---|
| Aligned (ADR-0016) | 482 | 1.37% | 3.76e-07 | 4.39e-10 | 0.097 | 102 of 1,852,394 |
