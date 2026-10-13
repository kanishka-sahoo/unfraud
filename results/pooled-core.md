## Core features: one model across datasets

Every payment Profile maps its dataset onto the same core features (amount in USD, channel, merchant category, hour, weekday) and computes the same real-time features (payer and payee velocity, unusual amounts, new payees, released fraud Labels). A model trained on them binds to any payment Profile.

Each cell is test PR-AUC / recall at a 0.5% Alert Budget. Italic cells are datasets the model never saw in training, scored unchanged; thresholds always come from the scored dataset's own validation period. Pooled models train on every listed dataset's training period at once.

| Trained on | tabformer | sparkov | amlworld |
|---|---|---|---|
| tabformer | 0.017 / 11% | *0.006 / 1%* | *0.002 / 2%* |
| sparkov | *0.001 / 0%* | 0.952 / 82% | *0.001 / 0%* |
| amlworld | *0.003 / 2%* | *0.006 / 0%* | 0.493 / 66% |
| tabformer + sparkov | 0.011 / 10% | 0.936 / 81% | *0.005 / 9%* |
| all three | 0.012 / 10% | 0.926 / 81% | 0.476 / 68% |
| *(guessing: the test fraud rate)* | 0.001 / 0.5% | 0.004 / 0.5% | 0.001 / 0.5% |
