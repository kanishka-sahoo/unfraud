## ieee-cis: LightGBM, time split (test = last month)

Thresholds for each Alert Budget are set on the validation month and applied unchanged to the test month. Label delay 720h0m0s.

| Configuration | PR-AUC | ROC-AUC | Recall @ 0.5% | Recall @ 1.0% | Recall @ 2.0% | Recall @ 5.0% | $-recall @ 5% | Precision @ 5% | Trees |
|---|---|---|---|---|---|---|---|---|---|
| Model, static features | 0.5545 | 0.8982 | 14.0% | 25.2% | 41.8% | 61.8% | 59.3% | 38.1% | 2541 |
| + real-time features | 0.6187 | 0.9229 | 13.4% | 27.3% | 44.9% | 66.4% | 61.7% | 42.7% | 1414 |

Recall ceilings (fraud a perfect model could catch at each budget): 0.5% → 15%, 1.0% → 28%, 2.0% → 58%, 5.0% → 100%.

Split: train 417,559 Events (3.53% fraud), val 83,655 Events (3.38% fraud), test 89,326 Events (3.49% fraud).
Features table sha256 `346314430ad79ad8…`, LightGBM 4.7.0, featurize at `051ecd0d33ce`.
