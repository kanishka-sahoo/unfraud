## Case Summaries

| Driver | Model | Cases | Grounded | Grounded on retry | Errors | Latency p50 / p99 (ms) | Cost per 1k Cases (USD) |
|---|---|---|---|---|---|---|---|
| stub | template | 34810 | 100% | 0 | 0 | 0 / 0 | 0.0000 |

### Sample for human grading (accuracy, usefulness, brevity: 1-5 each)

- **ieee-cis/3026164** (template): Case ieee-cis/3026164 was sent to block with a fraud probability of 0.9944, flagged by the graph revision. Strongest named factors: addr1 (122) raised the risk; TransactionAmt (335) raised the risk.
- **ieee-cis/3185488** (template): Case ieee-cis/3185488 was sent to review with a fraud probability of 0.7929, flagged by the graph revision. Strongest named factors: TransactionAmt (150) raised the risk; email_fraud_labels_30d (1180) raised the risk; DeviceInfo (rv:11.0) raised the risk.
- **ieee-cis/3440473** (template): Case ieee-cis/3440473 was sent to review with a fraud probability of 0.0009, flagged by the graph revision. Strongest named factors: card_fraud_labels_90d (3) raised the risk; card6 (debit) lowered the risk; graph_fraud_cards (77) raised the risk.
- **ieee-cis/3410970** (template): Case ieee-cis/3410970 was sent to review with a fraud probability of 0.9771, flagged by the graph revision. Strongest named factors: DeviceInfo (MacOS) lowered the risk.
- **ieee-cis/3232015** (template): Case ieee-cis/3232015 was sent to review with a fraud probability of 0.0016, flagged by the graph revision. Strongest named factors: card_fraud_labels_90d (1) raised the risk; card_fraud_labels_30d (1) raised the risk; P_emaildomain (hotmail.com) lowered the risk.
- **ieee-cis/3515387** (template): Case ieee-cis/3515387 was sent to review with a fraud probability of 0.5193, flagged by the real-time scorer on arrival. Strongest named factors: TransactionAmt (500) raised the risk; R_emaildomain (gmail.com) raised the risk; card6 (credit) raised the risk.
- **ieee-cis/3002283** (template): Case ieee-cis/3002283 was sent to review with a fraud probability of 0.0029, flagged by the real-time scorer on arrival. Strongest named factors: card6 (credit) raised the risk; P_emaildomain (hotmail.com) lowered the risk; card_amount_ratio_7d (5.6692) lowered the risk.
- **ieee-cis/3460611** (template): Case ieee-cis/3460611 was sent to review with a fraud probability of 0.0024, flagged by the graph revision. Strongest named factors: DeviceInfo (Trident/7.0) lowered the risk; email_fraud_labels_30d (1507) raised the risk; ProductCD (R) lowered the risk.
- **ieee-cis/3303743** (template): Case ieee-cis/3303743 was sent to review with a fraud probability of 0.9236, flagged by the graph revision. Strongest named factors: email_fraud_labels_30d (27) raised the risk; P_emaildomain (comcast.net) raised the risk; dist1 (0) raised the risk.
- **elliptic/139232036** (template): Case elliptic/139232036 was sent to review with a fraud probability of 0.9999, flagged by the real-time scorer on arrival.
