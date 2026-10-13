## amlworld: LightGBM, time split

Thresholds for each Alert Budget are set on the validation period and applied unchanged to the test period, so the share of test Events flagged can drift from the budget; the alert-rate column shows by how much. Label delay 72h0m0s.

| Configuration | PR-AUC | ROC-AUC | Recall @ 0.1% | Recall @ 0.5% | Recall @ 1.0% | Recall @ 2.0% | Test alert rate @ 0.5% | $-recall @ 0.5% | Precision @ 0.5% | Trees |
|---|---|---|---|---|---|---|---|---|---|---|
| Model, raw features (core + dataset extras) | 0.0819 | 0.9189 | 14.0% | 35.7% | 48.3% | 62.2% | 0.56% | 1.0% | 7.1% | 37 |
| + real-time features | 0.5118 | 0.9716 | 47.2% | 71.2% | 76.0% | 79.8% | 1.16% | 82.5% | 6.8% | 107 |
| + graph features | 0.4877 | 0.9757 | 39.3% | 55.5% | 61.9% | 70.3% | 0.25% | 57.8% | 24.8% | 963 |
| Core features only (shared by every payment Profile) | 0.4925 | 0.9797 | 44.9% | 65.7% | 72.9% | 76.5% | 0.54% | 87.8% | 13.4% | 533 |

Recall ceilings (fraud a perfect model could catch at each budget): 0.1% → 91%, 0.5% → 100%, 1.0% → 100%, 2.0% → 100%.

Split: train 3,248,921 Events (0.08% fraud), val 965,524 Events (0.11% fraud), test 862,792 Events (0.11% fraud).
Features table sha256 `80e3cc5f8857b8fc…`, LightGBM 4.7.0, featurize at `411d2af57e8e`.

### Decisions (scorer Accuracy Run, served model)

The scorer blocks above the 0.5%-budget threshold (0.006517) and sends to review above the 2%-budget threshold (0.002123); Rules can only escalate.

| Decision source | Alert rate | Precision | Recall | $-recall |
|---|---|---|---|---|
| Rules only | 0.02% | 19.5% | 4.3% | 1.0% |
| Model only (review + block) | 4.06% | 2.2% | 79.8% | 92.6% |
| Model + Rules | 4.08% | 2.2% | 79.8% | 92.6% |
| Model only, at the same alert volume as Model + Rules | 7.66% | 1.2% | 86.1% | 99.4% |
| Blocks only (model + rules) | 1.16% | 6.8% | 71.2% | 82.5% |

| Rule | Hits | Precision | Fraud caught |
|---|---|---|---|
| `FRAUD_PAYEE` | 210 | 19.5% | 41 |
