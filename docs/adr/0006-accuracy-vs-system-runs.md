# Fraud metrics come only from deterministic Accuracy Runs; System Runs measure speed and drift

Scaling scorers out against an asynchronous graph-svc makes graph features slightly stale and therefore makes scores vary from run to run. Accuracy Runs pause at each Watermark so every feature is exact as of Event Time, and all published fraud metrics come from them. System Runs use the production topology at full speed and report throughput, latency and score drift compared with the Accuracy Run, so the cost of staleness is measured rather than hidden.
