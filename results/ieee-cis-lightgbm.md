## ieee-cis: LightGBM, time split (test = last month)

Thresholds for each Alert Budget are set on the validation month and applied unchanged to the test month. Label delay 720h0m0s.

| Configuration | PR-AUC | ROC-AUC | Recall @ 0.5% | Recall @ 1.0% | Recall @ 2.0% | Recall @ 5.0% | $-recall @ 5% | Precision @ 5% | Trees |
|---|---|---|---|---|---|---|---|---|---|
| Model, static features | 0.5578 | 0.8970 | 13.8% | 24.9% | 42.1% | 62.8% | 57.2% | 38.8% | 2987 |
| + real-time features | 0.6109 | 0.9272 | 13.5% | 27.6% | 45.6% | 65.8% | 61.7% | 42.5% | 237 |
| + real-time features, unlimited depth (reference, not served) | 0.6187 | 0.9229 | 13.4% | 27.3% | 44.9% | 66.4% | 61.7% | 42.7% | 1414 |

Recall ceilings (fraud a perfect model could catch at each budget): 0.5% → 15%, 1.0% → 28%, 2.0% → 58%, 5.0% → 100%.

Split: train 417,559 Events (3.53% fraud), val 83,655 Events (3.38% fraud), test 89,326 Events (3.49% fraud).
Features table sha256 `346314430ad79ad8…`, LightGBM 4.7.0, featurize at `051ecd0d33ce`.

### Decisions (scorer Accuracy Run, served model)

The scorer blocks above the 0.5%-budget threshold (0.9784) and sends to review above the 2%-budget threshold (0.4502); Rules can only escalate.

| Decision source | Alert rate | Precision | Recall | $-recall |
|---|---|---|---|---|
| Rules only | 2.47% | 34.6% | 24.5% | 17.9% |
| Model only (review + block) | 2.05% | 77.5% | 45.6% | 34.3% |
| Model + Rules | 3.72% | 47.0% | 50.1% | 37.0% |
| Model only, at the same alert volume as Model + Rules | 3.93% | 53.0% | 59.7% | 54.1% |
| Blocks only (model + rules) | 0.89% | 81.7% | 20.9% | 15.6% |

| Rule | Hits | Precision | Fraud caught |
|---|---|---|---|
| `DEVICE_CARD_BURST` | 773 | 30.9% | 239 |
| `FRAUD_DEVICE` | 490 | 9.8% | 48 |
| `KNOWN_FRAUD_CARD` | 447 | 72.0% | 322 |
| `PRIOR_FRAUD_CARD` | 1,013 | 51.2% | 519 |

### System Run (in-cluster scorer, lite profile, 4-core / 8 GB machine)

One scorer pod scoring the whole input topic from a backlog, with features in Valkey, compared Event by Event with the Accuracy Run (`scripts/system-run.sh`, `cmd/drift`). End-to-end latency is not reported here: a backlog measures queueing, not scoring; it needs a live-paced replay.

| Scorer | Events/s | Probabilities differing | Mean abs diff | p99 abs diff | Max abs diff | Model decisions differing |
|---|---|---|---|---|---|---|
| Aligned (ADR-0016) | 1,067 | 6.66% | 1.12e-04 | 1.65e-03 | 0.163 | 80 of 590,540 |
| Unaligned (control) | 1,129 | 90.85% | 2.96e-03 | 5.14e-02 | 0.601 | 1,271 of 590,540 |
