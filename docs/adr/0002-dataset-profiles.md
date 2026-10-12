# One pipeline for all datasets through Profiles

Elliptic runs through the same streaming pipeline as IEEE-CIS and PaySim, not in a notebook. Rather than branching services per dataset, each dataset is a Profile: an adapter that emits the standard Event / Link / Watermark / LabelRelease schema, plus declarative entities, links, features, rules, model and Label Release delay. Services contain no dataset-specific code.

## Considered Options

- Separate pipelines per dataset: simpler at first, but the graph and scoring code would fork and the "one engine" claim would be false.
- Elliptic as an offline benchmark only: rejected by scope decision; it must exercise the real system.
