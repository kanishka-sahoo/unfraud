## tabformer: LightGBM, time split

Thresholds for each Alert Budget are set on the validation period and applied unchanged to the test period, so the share of test Events flagged can drift from the budget; the alert-rate column shows by how much. Label delay 720h0m0s.

| Configuration | PR-AUC | ROC-AUC | Recall @ 0.1% | Recall @ 0.5% | Recall @ 1.0% | Recall @ 2.0% | Test alert rate @ 0.5% | $-recall @ 0.5% | Precision @ 0.5% | Trees |
|---|---|---|---|---|---|---|---|---|---|---|
| Model, raw features | 0.7032 | 0.7811 | 47.2% | 75.8% | 75.9% | 75.9% | 0.48% | 73.6% | 19.0% | 1000 |
| + real-time features | 0.9584 | 1.0000 | 43.2% | 100.0% | 100.0% | 100.0% | 0.46% | 100.0% | 26.1% | 966 |
| + graph features | 0.9391 | 0.9999 | 59.2% | 100.0% | 100.0% | 100.0% | 0.47% | 100.0% | 25.9% | 1000 |

Recall ceilings (fraud a perfect model could catch at each budget): 0.1% → 50%, 0.5% → 100%, 1.0% → 100%, 2.0% → 100%.

Split: train 6,421,691 Events (0.14% fraud), val 433,579 Events (0.16% fraud), test 1,723,938 Events (0.12% fraud).
Features table sha256 `62a42eaf804e8a08…`, LightGBM 4.7.0, featurize at `7fafbc1f629d`.

### Calibration (test period)

Each model's raw scores are mapped to probabilities with Platt scaling fitted on the validation period (`train/src/unfraud_train/calibration.py`). It keeps the ranking, so PR-AUC and recall are unchanged. Expected fraud is the sum of the probabilities; ECE weights each bin's gap between mean probability and fraud rate by its share of Events, so it is dominated by the many Events near zero.

| Configuration | Slope | Intercept | Expected fraud (raw → calibrated) | Actual fraud | Log loss | Brier | ECE |
|---|---|---|---|---|---|---|---|
| Model, raw features | 0.677 | -0.091 | 1,771 → 2,078 | 2,087 | 0.00854 → 0.00611 | 0.000459 → 0.000456 | 4.00e-04 → 5.29e-04 |
| + real-time features | 0.567 | -0.019 | 2,181 → 2,297 | 2,087 | 0.00076 → 0.00066 | 0.000184 → 0.000172 | 1.32e-04 → 1.55e-04 |
| + graph features | 0.491 | -0.610 | 2,333 → 2,417 | 2,087 | 0.00159 → 0.00094 | 0.000231 → 0.000214 | 2.00e-04 → 2.39e-04 |

Reliability of the served model (+ real-time features), calibrated:

| Probability | Events | Mean probability | Fraud rate | Fraud |
|---|---|---|---|---|
| 0–0.0001 | 1,542,727 | 0.0018% | 0.0000% | 0 |
| 0.0001–0.001 | 156,758 | 0.0267% | 0.0000% | 0 |
| 0.001–0.01 | 19,692 | 0.2628% | 0.0000% | 0 |
| 0.01–0.05 | 1,906 | 2.0023% | 0.1049% | 2 |
| 0.05–0.1 | 248 | 6.9955% | 2.0161% | 5 |
| 0.1–0.2 | 179 | 14.0410% | 17.8771% | 32 |
| 0.2–0.5 | 241 | 34.9913% | 43.9834% | 106 |
| 0.5–1 | 2,187 | 91.9821% | 88.7974% | 1,942 |

### Decisions (scorer Accuracy Run, served model)

The scorer blocks above the 0.5%-budget threshold (0.004185) and sends to review above the 2%-budget threshold (0.0006947); Rules can only escalate.

| Decision source | Alert rate | Precision | Recall | $-recall |
|---|---|---|---|---|
| Rules only | 0.30% | 15.2% | 38.1% | 34.7% |
| Model only (review + block) | 1.93% | 6.3% | 100.0% | 100.0% |
| Model + Rules | 2.16% | 5.6% | 100.0% | 100.0% |
| Model only, at the same alert volume as Model + Rules | 2.09% | 5.8% | 100.0% | 100.0% |
| Blocks only (model + rules) | 0.46% | 26.1% | 100.0% | 100.0% |

| Rule | Hits | Precision | Fraud caught |
|---|---|---|---|
| `FRAUD_PAYEE` | 5,242 | 15.2% | 795 |

### System Run (in-cluster scorer, lite profile, 4-core / 8 GB machine)

One scorer pod scoring the whole input topic from a backlog, with features in Valkey, compared Event by Event with the Accuracy Run (`scripts/system-run.sh`, `cmd/drift`). End-to-end latency is not reported here: a backlog measures queueing, not scoring; it needs a live-paced replay.

| Scorer | Events/s | Probabilities differing | Mean abs diff | p99 abs diff | Max abs diff | Model decisions differing |
|---|---|---|---|---|---|---|
| Aligned (ADR-0016) | 468 | 6.23% | 8.94e-07 | 2.06e-06 | 0.231 | 116 of 1,500,000 |
