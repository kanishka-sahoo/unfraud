# graph-svc revises Decisions; the scorer never reads graph features

The design had graph-svc publish per-Component statistics to Valkey for the scorer to read on every Event. That couples the two services' progress: graph-svc running ahead would leak the future into the scorer, running behind would serve stale structure, and there is no exact Accuracy Run to compare against. Instead:

- The **scorer** stays partition-parallel and makes **Provisional** Decisions without graph features (IEEE-CIS: real-time model; Elliptic: local features).
- **graph-svc** is the single writer (ADR-0007). It reads every partition in Event Time order, keeps exact in-memory feature and graph state, and writes **Revised** Decisions to `revised.<profile>` using a graph-aware model: on every Event for IEEE-CIS (the attribute graph is complete on arrival), and when each timestep closes for Elliptic (flow Links are complete only then).
- graph-svc recovers by recomputation: on start it reads the input topic from the beginning and emits only after the offsets it committed last. At IEEE-CIS scale that takes under 3 minutes and needs no snapshot store.

Because graph-svc is one ordered process, its output can be exact. With Envelope sequence numbers breaking Event Time ties (ADR-0016), its Revised Decisions are bit-identical to the Accuracy Run: 0 differences on all 590,540 IEEE-CIS and 203,769 Elliptic Events. The Elliptic Provisional scorer is also exact, since Elliptic has no cross-entity real-time features.

## Consequences

- Revised Decisions arrive after Provisional ones: milliseconds later for IEEE-CIS, when the timestep closes for Elliptic. Case handling must take the stricter of the two.
- graph-svc throughput (about 3,600 IEEE-CIS Events/s on one core) is the ceiling for Revisions; it scales up, not out.
