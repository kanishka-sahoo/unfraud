# LLM Summaries go through OpenRouter, asynchronously, and must pass a Grounding check

The summarizer is a separate consumer and never affects a Decision. OpenRouter is the only gateway, so any model can be selected per Profile and per Case type. Benchmark runs pin `provider.order`, disable fallbacks and use temperature 0, and every Summary records the model and provider OpenRouter actually used. Summaries whose numbers or Entities don't appear in the Case Packet are regenerated once, then dropped. A `stub` driver lets the whole system and `make bench` run without an API key.

## Consequences

- LLM output isn't bit-reproducible, so the Summaries themselves are published as artifacts; Grounding pass rate, cost and latency are the reproducible metrics.
