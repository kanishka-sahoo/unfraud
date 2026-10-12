# No runtime schema registry; compiled Protobuf plus buf breaking checks

The original design included Apicurio. On the 8 GB reference machine it would cost about 300 MB for no benefit: every producer and consumer is built from this repo at the same commit, from one buf module. Compatibility is enforced at build time with `buf breaking` in CI, and at runtime by the Feature Schema's `schema_id` (ADR-0013). Revisit this if a service outside this repo ever needs to consume the topics.
