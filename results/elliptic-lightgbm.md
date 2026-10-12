## elliptic: LightGBM, temporal split

Train timesteps 1–29 (26,381 labelled, 2,871 illicit), validation 30–34 (3,513, 591), test 35–49 (16,670, 1,083). The test period matches Weber et al. 2019, who trained on 1–34; timesteps 30–34 are held out here for early stopping and thresholds. Unknown-class transactions are scored but never evaluated. The timestep itself is not a feature.

| Configuration | PR-AUC | Illicit F1 @ 0.5 | Precision | Recall | F1 @ validation-best threshold | Trees |
|---|---|---|---|---|---|---|
| Provisional: local features, scored on arrival | 0.766 | 0.734 | 0.802 | 0.677 | 0.731 | 227 |
| Revised: + flow-graph features computed by the stream when the timestep closes | 0.793 | 0.780 | 0.849 | 0.722 | 0.779 | 422 |
| Reference: + the dataset's 72 precomputed neighbour aggregates | 0.792 | 0.789 | 0.866 | 0.724 | 0.743 | 190 |

Published reference points on the same test period (Weber et al. 2019, illicit F1): Random Forest on all features 0.788; GCN 0.628.

### Per test timestep (illicit F1 at each model's validation-best threshold)

| Timestep | Illicit | local | flow | dataset_agg |
|---|---|---|---|---|
| 35 | 182 | 0.923 | 0.967 | 0.949 |
| 36 | 33 | 0.753 | 0.780 | 0.750 |
| 37 | 40 | 0.754 | 0.776 | 0.727 |
| 38 | 111 | 0.884 | 0.916 | 0.917 |
| 39 | 81 | 0.865 | 0.919 | 0.874 |
| 40 | 112 | 0.667 | 0.736 | 0.755 |
| 41 | 116 | 0.856 | 0.918 | 0.921 |
| 42 | 239 | 0.814 | 0.857 | 0.830 |
| 43 | 24 | 0.000 | 0.000 | 0.000 |
| 44 | 24 | 0.042 | 0.043 | 0.071 |
| 45 | 5 | 0.000 | 0.000 | 0.000 |
| 46 | 2 | 0.087 | 0.125 | 0.080 |
| 47 | 22 | 0.000 | 0.000 | 0.000 |
| 48 | 36 | 0.000 | 0.000 | 0.050 |
| 49 | 56 | 0.031 | 0.032 | 0.030 |

Every model fails from timestep 43, when a large dark market shut down (Weber et al. 2019): the illicit patterns learned earlier stop appearing. This is reported, not hidden.
