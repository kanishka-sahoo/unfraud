# The scorer aligns partitions in Event Time before scoring

Scorer workers on different partitions share feature state in Valkey, and many features cross partitions (cards per device, fraud Labels per device, email or address). If each partition's worker runs through its fetched batch independently, then while the scorer works through a backlog the partitions drift apart in Event Time, and Events on a partition that is behind see other partitions' future.

The scorer buffers fetched records per partition and releases them through `internal/align` in (Event Time, partition, offset) order, never past the earliest frontier of a partition that still has unread data (a caught-up partition does not hold the others back). Released records are scored in chunks of 64, partition-parallel within a chunk, so only Events inside one chunk can see each other out of order. Partitions with full buffers are paused. Feature state expiry runs 6 hours of Event Time behind the Event being processed; expiring later never changes results, because expired state is invisible to queries.

## Evidence

System Runs over the full IEEE-CIS backlog (590,540 Events, one scorer pod, three partitions), each compared Event by Event with the Accuracy Run (`scripts/system-run.sh`, `cmd/drift`):

| Scorer | Events/s | Probabilities differing | Mean / p99 abs diff | Max abs diff | Model Decisions differing |
|---|---|---|---|---|---|
| Aligned | 1,067 | 6.7% | 0.00011 / 0.0016 | 0.16 | 80 |
| Unaligned (`-align=false`) | 1,129 | 91% | 0.0030 / 0.051 | 0.60 | 1,271 |

Alignment costs about 6% throughput and removes 94% of Decision drift.

The first measurement blamed all drift on misalignment and was wrong: the input topic had been written by an older replayer whose Label Releases lacked Entities, and the scorer was restarting after out-of-memory kills, re-applying feature updates on redelivery. Both are fixed (the engine now rejects such Label Releases, and updates carry idempotency Guards) and the table above was measured afterwards.

## Considered Options

- One worker for all partitions in strict Event Time order: no drift, but no parallelism, and it still needs the buffering above.
- No alignment: live traffic keeps partitions close, but drift appears exactly when the scorer falls behind (after an outage or under load), which is when fraud teams most need consistent scores.
