## tabformer: LightGBM, time split

Thresholds for each Alert Budget are set on the validation period and applied unchanged to the test period, so the share of test Events flagged can drift from the budget; the alert-rate column shows by how much. Label delay 720h0m0s.

| Configuration | PR-AUC | ROC-AUC | Recall @ 0.1% | Recall @ 0.5% | Recall @ 1.0% | Recall @ 2.0% | Test alert rate @ 0.5% | $-recall @ 0.5% | Precision @ 0.5% | Trees |
|---|---|---|---|---|---|---|---|---|---|---|
| Model, raw features (core + dataset extras) | 0.4569 | 0.5209 | 43.2% | 51.5% | 51.5% | 51.5% | 0.46% | 47.4% | 13.7% | 15 |
| + real-time features | 0.7625 | 0.9965 | 56.2% | 90.9% | 94.7% | 97.2% | 0.47% | 91.1% | 23.5% | 1000 |
| + graph features | 0.3458 | 0.9821 | 38.4% | 70.1% | 80.0% | 88.2% | 1.00% | 73.3% | 8.5% | 999 |
| Core features only (shared by every payment Profile) | 0.0171 | 0.9224 | 1.5% | 11.4% | 20.9% | 36.0% | 0.49% | 14.3% | 2.8% | 119 |

Recall ceilings (fraud a perfect model could catch at each budget): 0.1% → 55%, 0.5% → 100%, 1.0% → 100%, 2.0% → 100%.

Split: train 5,133,655 Events (0.14% fraud), val 1,721,615 Events (0.14% fraud), test 1,723,938 Events (0.12% fraud).
Features table sha256 `cb88ce79ade60395…`, LightGBM 4.7.0, featurize at `411d2af57e8e`.

### Decisions (scorer Accuracy Run, served model)

The scorer blocks above the 0.5%-budget threshold (7.004e-05) and sends to review above the 2%-budget threshold (2.878e-06); Rules can only escalate.

| Decision source | Alert rate | Precision | Recall | $-recall |
|---|---|---|---|---|
| Rules only | 0.30% | 15.2% | 38.1% | 34.7% |
| Model only (review + block) | 1.98% | 5.9% | 97.2% | 96.9% |
| Model + Rules | 2.22% | 5.3% | 98.2% | 97.8% |
| Model only, at the same alert volume as Model + Rules | 2.20% | 5.4% | 97.5% | 97.1% |
| Blocks only (model + rules) | 0.47% | 23.5% | 90.9% | 91.1% |

| Rule | Hits | Precision | Fraud caught |
|---|---|---|---|
| `FRAUD_PAYEE` | 5,242 | 15.2% | 795 |
