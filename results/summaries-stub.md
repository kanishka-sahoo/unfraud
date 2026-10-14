## Case Summaries

| Driver | Model | Cases | Grounded | Grounded on retry | Errors | Latency p50 / p99 (ms) | Cost per 1k Cases (USD) |
|---|---|---|---|---|---|---|---|
| stub | template | 49493 | 100% | 0 | 0 | 0 / 0 | 0.0000 |

### Sample for human grading (accuracy, usefulness, brevity: 1-5 each)

- **tabformer/11270836** (template): Case tabformer/11270836 was sent to review with a fraud probability of 0.001, flagged by the graph revision. Strongest named factors: payer_new_payee (1) raised the risk; mcc (5300) raised the risk; hour (9) raised the risk.
- **tabformer/13516556** (template): Case tabformer/13516556 was sent to block with a fraud probability of 0.0094, flagged by the real-time scorer on arrival. Strongest named factors: payee_since_last (missing) raised the risk; payee_payers_7d (0) raised the risk; payer_channel_changed (1) raised the risk.
- **tabformer/20999980** (template): Case tabformer/20999980 was sent to review with a fraud probability of 0.0023, flagged by the real-time scorer on arrival. Strongest named factors: merchant_state (missing) raised the risk; hour (12) raised the risk; mcc (5311) raised the risk.
- **tabformer/12959112** (template): Case tabformer/12959112 was sent to block with a fraud probability of 0.1418, flagged by the real-time scorer on arrival. Strongest named factors: mcc (5912) raised the risk; payer_new_payee (1) raised the risk; payee_since_last (missing) raised the risk.
- **tabformer/11978863** (template): Case tabformer/11978863 was sent to review with a fraud probability of 0.0011, flagged by the graph revision. Strongest named factors: payer_new_payee (1) raised the risk; hour (12) raised the risk; payee_payers_7d (0) raised the risk.
- **tabformer/21417199** (template): Case tabformer/21417199 was sent to review with a fraud probability of 0.0032, flagged by the real-time scorer on arrival. Strongest named factors: hour (10) raised the risk; payer_new_payee (1) raised the risk; payee_payers_7d (0) raised the risk.
- **tabformer/1114553** (template): Case tabformer/1114553 was sent to review with a fraud probability of 0.0007, flagged by the real-time scorer on arrival. Strongest named factors: payer_new_payee (1) raised the risk; payer_count_7d (6) raised the risk; payer_amount_24h (366.98) raised the risk.
- **tabformer/13370347** (template): Case tabformer/13370347 was sent to review with a fraud probability of 0.0011, flagged by the real-time scorer on arrival. Strongest named factors: mcc (5300) raised the risk; payee_since_last (missing) raised the risk; payer_new_payee (1) raised the risk.
- **tabformer/6099876** (template): Case tabformer/6099876 was sent to review with a fraud probability of 0.003, flagged by the real-time scorer on arrival. Strongest named factors: mcc (5912) raised the risk; payer_new_payee (1) raised the risk; payee_payers_7d (1) raised the risk.
- **tabformer/21982008** (template): Case tabformer/21982008 was sent to review with a fraud probability of 0.0022, flagged by the graph revision. Strongest named factors: channel (online) raised the risk; mcc (4722) lowered the risk; merchant_state (missing) raised the risk.
