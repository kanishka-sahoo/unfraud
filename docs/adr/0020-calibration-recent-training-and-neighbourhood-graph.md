# Calibrated probabilities, a recent training window and neighbourhood graph features

Two problems showed up once TabFormer was the only dataset:

- **The probabilities meant nothing.** The served model blocked above a fraud probability of 0.007% and sent payments to review above 0.0003%, so most Cases read "fraud probability <0.1%". The model ranked payments well, but deep boosted trees fitted to the training years were overconfident on later ones.
- **Graph features lowered accuracy** (test PR-AUC 0.763 → 0.346, validation 0.834 → 0.655).

## Decision

**Calibration.** Each model's raw score is mapped to a probability by Platt scaling, `sigmoid(slope × raw + intercept)`, fitted on the validation period and stored in the model manifest. The Go model applies it (`model.Score`), and TreeSHAP Contributions are scaled by the slope with the intercept added to the bias, so the risk ledger still adds up to the Case's log-odds. Platt scaling is monotone, so rankings, PR-AUC and the Events flagged at each Alert Budget are unchanged.

**Neighbourhood graph features.** Chains of shared merchants put about 3,800 cards, most of the active ones, into one connected Component every day. Its size and fraud count only tracked the population and the overall fraud level, and the model learned those as a clock. The graph features now describe the Event's neighbourhood:

- the payee: other cards that used it in the last 30 days with released fraud, as a count and a share;
- the neighbours: cards sharing any of the card's small (non-Super-node) merchants, as a count and a share with released fraud;
- the Louvain Community: its size and the share of its other cards with released fraud.

The five fraud features are trained with monotone constraints: more confirmed fraud nearby may only raise risk. Without them the 2016 fraud wave, when high merchant fraud shares came with lower risk, taught the model the opposite of what holds later. On validation the constraint raised the graph model from 0.923 to 0.938.

**A recent training window.** Training now runs from 2015 to September 2018. The last quarter of 2018 validates: early stopping, calibration and thresholds. 2019 still tests. Fraud before 2018 looks different. 2016 had a wave (3,579 fraud Events), 2017 almost none (255). A neighbourhood signal that appears in 2018 is absent earlier: payments at merchants where more than 10% of the other recent cards had confirmed fraud were fraud 3–5% of the time in 2018–2019, against the 0.1–0.2% base rate. A model trained only on 2015–2017 missed most of it.

## How we got there

New graph features alone did not fix the graph model on the old split (test 0.437, validation 0.559). A second-stage model fitted on 2018 on top of the real-time model's score did much better. A control fitted the same way without graph features did just as well, though: on held-out late 2018 PR-AUC rose from 0.856 to 0.9414 without graph features and to 0.9415 with them. The gain came from recent data, not from the graph, so the fix is the training window for every model rather than a graph-specific stage.

That investigation printed 2019 PR-AUC alongside the late-2018 numbers, so 2019 is no longer an untouched holdout for this one choice. The choice itself rests on the late-2018 numbers, and the graph features and constraint were judged on validation alone.

## Considered Options

- **Isotonic calibration:** fits any monotone shape, but it ties scores within each step and is not linear in log-odds, so the risk ledger would stop adding up.
- **A graph revision stage fitted on 2018** (stacked on the real-time score): as above, the graph added 0.0001 PR-AUC on held-out late 2018 over the same stage without it. It would need a composite model in Go for no measurable gain.
- **Training on 2018 alone:** less data for the real-time features' rarer patterns, and one year cannot both train and validate.

## Consequences

- The real-time model's test PR-AUC goes from 0.763 to 0.958; the graph model's from 0.346 to 0.939. Graph features still do not beat the real-time model on validation (0.938 against 0.949), so the scorer serves the real-time model and graph-svc revises with the graph model as before.
- Validation is one quarter (433,579 Events, 710 fraud), so thresholds and calibration rest on fewer fraud cases than before.
- Rules were reviewed again on the new validation quarter, with the same outcome: `FRAUD_PAYEE` stays, the other five stay retired.
- Training runs up to the validation quarter, so its last 30 days of Labels would not have been released yet in a real retrain (the old split had the same property). A production retrain should leave a gap of one Label delay.
- The model manifest gains `calibration` and `test_calibration`, and the predictions files gain a calibrated `score` next to `raw_score`. Models without `calibration` score as before.
