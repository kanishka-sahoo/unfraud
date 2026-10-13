# Each dataset has one role: IEEE-CIS for fraud metrics, Elliptic for graph metrics, PaySim for load only

Status: superseded by [ADR-0018](./0018-named-payment-datasets.md).

The three public datasets don't share Entities and differ in honesty. IEEE-CIS is the only one with both velocity signals and shared attributes, so it carries every fraud metric. Elliptic is a real labeled flow graph but has no devices, IPs or cards, so it carries the graph metrics. PaySim is synthetic, its balance columns give away the label, and its origin accounts almost never repeat, so it is used only to load-test throughput. We claim "reproducible from public data", never "real".

## Consequences

- PaySim fraud metrics are never published, even though they would look impressive.
- "How far a card moved" is limited to IEEE-CIS address changes and `dist1` deltas; there is no geo-velocity.
- Licences differ (IEEE-CIS: competition rules, no redistribution; PaySim: CC BY-SA 4.0; Elliptic: CC BY-NC-ND 4.0), so the data is never committed. `make data` downloads it with the user's own Kaggle credentials and verifies it by checksum.
