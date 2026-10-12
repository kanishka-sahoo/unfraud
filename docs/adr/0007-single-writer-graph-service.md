# graph-svc is a single writer with a hot standby, not horizontally partitioned

Global union-find and community detection don't partition cleanly: a Ring can span any Kafka partition. graph-svc runs as a single writer with a hot standby, snapshots to object storage and rebuilds from compacted topics. IEEE-CIS (590k Events) and Elliptic (204k nodes) fit in memory many times over. The scaling limit is a known, documented ceiling, not an oversight.

## Considered Options

- A graph database (Neo4j or Memgraph): adds an operationally heavy component, and an in-process graph is faster for our access patterns.
- Partitioning by Component: Components merge across partitions, which needs distributed coordination we don't need at this scale.
