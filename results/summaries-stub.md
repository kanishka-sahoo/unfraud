## Case Summaries

| Driver | Model | Cases | Grounded | Grounded on retry | Errors | Latency p50 / p99 (ms) | Cost per 1k Cases (USD) |
|---|---|---|---|---|---|---|---|
| stub | template | 78163 | 100% | 0 | 0 | 0 / 0 | 0.0000 |

### Sample for human grading (accuracy, usefulness, brevity: 1-5 each)

- **tabformer/11270836** (template): Case tabformer/11270836 was sent to review with a fraud probability of 0, flagged by the real-time scorer on arrival. Strongest named factors: mcc (5300) raised the risk; payer_new_payee (1) raised the risk; hour (9) raised the risk.
- **tabformer/3357500** (template): Case tabformer/3357500 was sent to review with a fraud probability of 0, flagged by the graph revision. Strongest named factors: merchant_state (missing) raised the risk; payee_payers_7d (237) lowered the risk; payer_new_payee (1) raised the risk.
- **tabformer/3043744** (template): Case tabformer/3043744 was sent to review with a fraud probability of 0, flagged by the graph revision. Strongest named factors: merchant_state (missing) raised the risk; abroad (missing) raised the risk; payee_amount_ratio_7d (0.0867) raised the risk.
- **tabformer/8831783** (template): Case tabformer/8831783 was sent to review with a fraud probability of 0, flagged by the graph revision. Strongest named factors: hour (10) raised the risk; mcc (5300) raised the risk; payee_payers_7d (1) raised the risk.
- **tabformer/23949004** (template): Case tabformer/23949004 was sent to review with a fraud probability of 0, flagged by the real-time scorer on arrival. Strongest named factors: abroad (missing) raised the risk; merchant_state (missing) raised the risk; payer_since_last (649920) lowered the risk.
- **tabformer/13516556** (template): Case tabformer/13516556 was sent to block with a fraud probability of 0.0002, flagged by the real-time scorer on arrival. Strongest named factors: payee_since_last (missing) raised the risk; payer_new_payee (1) raised the risk; payer_count_7d (3) raised the risk.
- **tabformer/20999980** (template): Case tabformer/20999980 was sent to block with a fraud probability of 0, flagged by the graph revision. Strongest named factors: merchant_state (missing) raised the risk; hour (12) raised the risk; payer_new_payee (1) raised the risk.
- **tabformer/12959112** (template): Case tabformer/12959112 was sent to block with a fraud probability of 0.0344, flagged by the real-time scorer on arrival. Strongest named factors: mcc (5912) raised the risk; payer_new_payee (1) raised the risk; payee_since_last (missing) raised the risk.
- **tabformer/11978863** (template): Case tabformer/11978863 was sent to review with a fraud probability of 0, flagged by the graph revision. Strongest named factors: payer_new_payee (1) raised the risk; payee_since_last (missing) raised the risk; payee_payers_7d (0) raised the risk.
- **tabformer/21417199** (template): Case tabformer/21417199 was sent to review with a fraud probability of 0, flagged by the real-time scorer on arrival. Strongest named factors: hour (10) raised the risk; payer_new_payee (1) raised the risk; weekday (0) raised the risk.
