# Each dataset has one role: IEEE-CIS for fraud metrics, Elliptic for graph metrics, PaySim for load only

The three public datasets don't share Entities and differ in honesty. IEEE-CIS is the only one with both velocity signals and shared attributes, so it carries every fraud metric. Elliptic is a real labeled flow graph but has no devices, IPs or cards, so it carries the graph metrics. PaySim is synthetic, its balance columns give away the label, and its origin accounts almost never repeat, so it is used only to load-test throughput. We claim "reproducible from public data", never "real".

## Consequences

- PaySim fraud metrics are never published, even though they would look impressive.
- "How far a card moved" is limited to IEEE-CIS address changes and `dist1` deltas; there is no geo-velocity.
- Kaggle licences forbid redistribution, so datasets are downloaded with the user's own credentials and verified by checksum.
