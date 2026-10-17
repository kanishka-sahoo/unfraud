# Summaries are written on request; analysts dig deeper in an Investigation

ADR-0021 summarized every blocked Case as it arrived, 30 a minute at most. At replay rates of 1,000+ Events a second the queue never drains: nearly all the spend goes to Cases nobody opens, and a Summary an analyst asks for waits behind a backlog that keeps growing. A Summary built from the 72-hour Case Packet also says little that the console doesn't already show.

## Decision

**Summaries only on request.** The summarizer claims a Case only when an analyst asks for its Summary; nothing is summarized on arrival. New Cases start with no summary status (`none`) instead of `pending`. ADR-0009's rules still hold: off the hot path, the Grounding check with one retry, and model, tokens, cost and latency recorded.

**A richer Case Packet.** One read-only context layer (`internal/investigate`) gathers what an analyst would look up next, as described facts that the Grounding check can check:

- the card's history over 30 days: alerts, Dispositions, amounts by merchant type;
- the merchant and merchant type: recent Cases and released fraud;
- the network: ring membership, neighbours with released fraud and the Community, from graph-svc's neighbourhood;
- related Cases (same card or merchant, nearby in Event Time) and their Dispositions;
- the Event's Decisions, Provisional and Revised.

**Summary v2.** A fixed workflow rather than an agent: gather the Packet, write, check Grounding, retry once. It has sections: what happened, the pattern, related context, and what to check next. It never suggests a Disposition (ADR-0011). It stays on Claude Haiku and keeps all three drivers, so it can still be benchmarked.

**Investigations.** The console has an assistant sidebar that works like any agent chat: the analyst asks in plain words, and the agent reasons over several steps, looks things up and answers. Most users are fraud analysts, not developers, so everything they see is in their own terms: cards, merchants, payments and alerts, never JSON, SQL, feature names or tool names.

- **Open from anywhere.** The sidebar is on every page. A new Investigation starts from whatever is on screen: an alert brings in its Case Packet and Summary, the network page brings in the card, and other pages start blank. It can then follow the evidence to other Cases and cards.
- **Multi-step answers.** The agent plans, calls tools, reads what they return and calls more before it answers, with up to 8 lookups per question. While it works, the sidebar shows each step as a plain sentence ("Looked at card 77-1's last 30 days: 14 alerts, 3 confirmed fraud"). The steps collapse into "How I worked this out" once the answer arrives. Opening a step shows its data as a small table with links to the alerts and cards; there is no raw output. The model's own reasoning is not shown: the steps are the explanation.
- **Chat controls:**
  - *Stop* cancels a running answer.
  - *Edit and resend* rewinds to any earlier question: everything after it is set aside, and the edited question runs again.
  - *Retry* reruns the last answer.
  - *New chat* clears the sidebar. Earlier Investigations stay listed under the Case (or under "Recent") and can be reopened.
  - *Copy* copies an answer.
- **Helpful empty state.** Suggested questions fit the page, such as "Has this card been flagged before?", "Is this merchant seeing other fraud?" or "Who else is connected to this card?". Errors are in plain words ("I couldn't reach the network data just now, so this answer leaves it out"), and the agent says so when the data can't answer a question.
- **Checked numbers.** A chat answer can't be rejected and retried the way a Summary is. Instead, every number and identifier in it is checked against what the agent looked up in that Investigation. Anything it couldn't verify gets a quiet "not verified" mark with an explanation on hover. An answer is never hidden.
- **Read-only.** The agent can't set or draft a Disposition (ADR-0011). It may say what to check, and the analyst decides.
- **Where:** the summarizer becomes the `assistant` service. It works the Summary queue and serves Investigations over SSE, which cases-api proxies at `/api/investigations/...`. API keys, drivers and metrics stay in one place, and the lite profile gets no new pod.
- **Model:** Claude only, Haiku 5.5 (`claude-haiku-5-5`) by default (`assistant.chat.model`; Sonnet 5.5 reasons further), through the Anthropic SDK's tool use with adaptive thinking at `effort: medium`. Thinking blocks are stored and sent back with their tool results, as the API requires, but are never displayed. Prompt caching covers the system prompt, the tools and the conversation so far. On the Claude API with Sonnet or Opus, a declined request is re-served by a fallback model the API chooses (`fallbacks: "default"`), so analysts aren't left without an answer.
- **Tools:** read-only, within one Profile: `get_alert` (the full Case Packet, both Stages' Decisions included), `search_alerts`, `card_history`, `merchant_activity`, `card_network` and `related_alerts`. Each returns described facts for the model plus a one-line plain description and rows for the UI. Windows end at the time being investigated, in dataset time.
- **Bounds:** at most 8 lookups per question, a token budget per Investigation (the sidebar suggests a new chat when it runs out), and a global rate limit.
- **Storage:** `investigations` (id, Profile, opening Case or card, title, opening context, created and updated) and `investigation_messages` (sequence number, role, content blocks with thinking, tool calls and results, what the console shows for each lookup, model, tokens, cost, and `superseded_at`). Content is the Messages API's own JSON in a `text` column: jsonb reorders keys, and a thinking block is only valid in the exact conversation that produced it. A rewind marks later messages superseded instead of deleting them, so every Investigation can be audited, and what is sent to Claude stays an unchanged prefix plus new messages. A stopped or failed answer is stored as a short assistant message, so the conversation can continue.
- **Without a key:** a stub agent plays a scripted sequence of steps, so the e2e tests and `make bench` still run without an API key.

## Consequences

- Most Cases never get a Summary. The console offers "Write summary" instead of showing a template.
- A Summary must have at least a sentence and one supporting point: Haiku once returned a one-character placeholder, which passes Grounding trivially.
- Investigation answers are checked but not guaranteed grounded; the UI marks what it couldn't verify.
- Rewound messages are kept, and the stored history includes them, so audits see everything the analyst saw.
- New metrics: Grounding rate for Summaries and for chat answers, tool calls per question, and cost per Investigation, published in `results/`.
