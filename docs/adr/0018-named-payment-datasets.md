# Named synthetic payment datasets, normalized into one Payment Schema

Supersedes [ADR-0001](./0001-dataset-roles.md). Its dataset choice is superseded by [ADR-0019](./0019-tabformer-only.md).

IEEE-CIS and Elliptic were real but masked: most of their columns (V1–V339, C, D, M, id_*, Elliptic's 165 features) had no published meaning, so a Case could only report "the combined effect of masked features", rules could only use the handful of documented columns, and nothing learned on one dataset could be checked on another. Public payment data with readable columns exists only as simulations, so we accept synthetic data in exchange for meaning:

| Profile | Dataset | Role |
|---|---|---|
| `tabformer` | IBM TabFormer: 2,000 cardholders, several cards each, Oct 2014–Dec 2019 (9.0M Events) | card fraud, attribute graph (cards through merchants) |
| `sparkov` | Sparkov (Kaggle): 1,000 cardholders, 693 merchants, 2019–2020 (1.85M Events) | card fraud, no graph |
| `amlworld` | IBM AMLworld HI-Small: 500,000 bank accounts over ten days (5.08M Events) | laundering as fraud, attribute graph (accounts through receiving accounts) |
| `paysim` | PaySim | load testing only (its balance columns give away the label) |

`unfraud-train prepare` maps each dataset onto the **Payment Schema**: Event Time, Payer, Payee, Customer, amount in USD, channel, merchant category (TabFormer's MCCs mapped onto Sparkov's categories), label, then the dataset's named extras. One Go adapter (`internal/profile/payments`) reads all three, and every payment Profile computes the same real-time features under the same names. A **Core Model** trained on those features alone binds to any payment Profile (schema ID 0), so a model can be trained on one dataset, several pooled, or all, and scored on another.

## Considered Options

- **Keep IEEE-CIS and Elliptic.** Real data, but most features are unexplainable and nothing transfers.
- **Add Feedzai BAF.** Named columns, but they are one-off account applications with no Entities that repeat, so there is no stream to replay and no graph.
- **Train one model on all datasets and publish one pooled metric.** The datasets share no Entities, their base rates differ (0.1%–0.5%), and each simulator places fraud differently, so a pooled score partly measures which dataset an Event came from. Pooled models are trained, but always reported per dataset, beside models trained on each dataset alone and models scoring datasets they never saw.

## Consequences

- We claim "reproducible from public, synthetic data". Metrics on simulated fraud say how well the engine finds the simulator's patterns, not real-world performance.
- No Entity appears in more than one dataset, so the graph never links across them; "across datasets" means shared features and transferable models, not shared Entities.
- The flow graph (Elliptic) is gone; Revisions come only from Ring Alerts.
- `make data` downloads with the user's Kaggle credentials and records checksums of both the raw and the prepared files, so preparation is verified too. Licences: Sparkov CC0, AMLworld CDLA-Sharing-1.0, TabFormer per IBM's release (Apache-2.0 on GitHub; the Kaggle mirror says "other"), PaySim CC BY-SA 4.0. The data is never committed.
