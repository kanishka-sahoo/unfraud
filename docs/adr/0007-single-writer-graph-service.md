# graph-svc is a single writer, not horizontally partitioned

Global union-find and community detection don't partition cleanly: a Ring can span any Kafka partition. graph-svc runs as a single writer (originally planned with a hot standby and snapshots; see the amendment). IEEE-CIS (590k Events) and Elliptic (204k nodes) fit in memory many times over. The scaling limit is a known, documented ceiling, not an oversight.

## Considered Options

- A graph database (Neo4j or Memgraph): adds an operationally heavy component, and an in-process graph is faster for our access patterns.
- Partitioning by Component: Components merge across partitions, which needs distributed coordination we don't need at this scale.

## Amendment (milestone 4)

There is no hot standby and no snapshot store. graph-svc runs as one replica with `Recreate` rollouts and recovers by recomputation: on start it re-reads its input topic from the beginning and emits only after the offsets it committed (ADR-0017). That takes under 3 minutes at IEEE-CIS scale, so a standby would buy little on the reference machine and could not be validated there.
