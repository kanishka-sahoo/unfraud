# One ordered input topic per Profile, with positional features

Each Profile has a single topic, `input.<profile>`, whose records are an `Envelope` holding an Event, Link, Watermark or LabelRelease. Kafka orders records only within a partition, so separate topics per record type would lose the ordering that Watermarks and delayed Label Releases depend on. Records are keyed by the primary Entity, and each Watermark is copied to every partition so every consumer sees it.

Features travel as positional arrays (`repeated double num`, `repeated string cat`) laid out by the Profile's Feature Schema, rather than as name-to-value maps. With maps, every message repeated about 400 feature names, deterministic encoding sorted them, and every decode in the scorer's hot path would have allocated a string-keyed map. Switching tripled replay throughput (3.1k to 14.5k Elliptic events/s). A `schema_id` hash on every Event turns a producer/consumer schema mismatch into an explicit rejection instead of silently shifted features.

## Consequences

- Adding, removing or reordering a feature changes the `schema_id`, so input topics must be replayed again after a schema change.
- Consumers look features up by index, resolved once from the Profile's Feature Schema at startup.
