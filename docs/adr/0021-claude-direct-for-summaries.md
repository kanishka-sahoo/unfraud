# Summaries call Claude directly by default; any Anthropic- or OpenAI-compatible endpoint works

Summaries are now written only on request, by the assistant service ([ADR-0022](./0022-summaries-on-request-and-investigations.md)).

ADR-0009 made OpenRouter the only gateway so any model could be compared. In practice one model writes the Summaries analysts read, and operators hold different keys: an Anthropic key, an OpenRouter key, or a self-hosted model. The summarizer now has three API drivers, all held to ADR-0009's rules (off the hot path, the Grounding check with one retry, model, tokens, cost and latency recorded):

- `anthropic` (the default): the Messages API through the official Go SDK, with structured output (`output_config.format`). `baseURL` points it at any Anthropic-compatible API. The default model is Claude Haiku 5.5 (`claude-haiku-5-5`) at `effort: low`; it takes no `temperature`, and its adaptive thinking counts toward `max_tokens`, so requests leave room for it.
- `openai`: any OpenAI-compatible chat completions API at `baseURL` (OpenAI, vLLM, Ollama), with `json_schema` output. `temperature` is sent only when set.
- `openrouter`: the `openai` driver pointed at OpenRouter, adding its cost reporting and provider pinning (`anthropic/claude-haiku-5.5`).

Keys live in the repository's git-ignored `.env` (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`); `make secrets` turns them into Kubernetes Secrets, which the chart mounts as optional. Without its key, a driver logs a warning and writes template Summaries, which the console labels as such. To bound spend, only blocked Cases are summarized as they arrive (30 a minute at most); an analyst can ask for any Case's Summary from the console, and those requests jump the queue.

## Consequences

- Temperature 0 is no longer the default: Claude 5 models reject it. Benchmark runs that compare models through `openai` or `openrouter` set it explicitly where the model allows.
- Cost is computed from Anthropic's list price for the Claude API (Haiku 5.5: $0.10 / $0.50 per million tokens below 100,000 prompt tokens); other Anthropic-compatible endpoints report no cost, and only OpenRouter reports it for OpenAI-compatible calls.
- Review Cases arriving during a replay keep their template Summaries unless someone asks for one.
