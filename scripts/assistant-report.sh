#!/usr/bin/env bash
# Summarises the assistant chat (Investigations) per model: questions and how
# they ended, lookups per question, answers with unverified details, tokens
# and cost (ADR-0022). Rewound questions count: they were asked and paid for.
#   scripts/assistant-report.sh results/assistant
# PSQL overrides how psql is run, e.g. PSQL="psql postgres://localhost/unfraud -Atq".
set -euo pipefail
out=${1:-results/assistant}
psql() { ${PSQL:-kubectl -n unfraud exec cases-1 -c postgres -- psql -U postgres -d unfraud -Atq} "$@"; }

psql -c "
WITH m AS (
  SELECT investigation_id, seq, kind, content::jsonb AS content, meta,
         -- Each message belongs to the question asked last before it.
         max(seq) FILTER (WHERE kind = 'question') OVER (PARTITION BY investigation_id ORDER BY seq) AS question
  FROM investigation_messages),
q AS (
  SELECT investigation_id, question,
         coalesce(max(meta->>'model') FILTER (WHERE kind = 'reply'), 'none') AS model,
         CASE WHEN bool_or(kind = 'stopped' AND (meta->>'refused')::bool) THEN 'refused'
              WHEN bool_or(kind = 'stopped' AND (meta->>'stopped')::bool) THEN 'stopped'
              WHEN bool_or(kind = 'stopped') THEN 'error'
              WHEN bool_or(kind = 'reply' AND meta->>'stop_reason' <> 'tool_use') THEN 'answered'
              ELSE 'unfinished' END AS status,
         coalesce(sum(jsonb_array_length(content)) FILTER (WHERE kind = 'tool_results'), 0) AS lookups,
         bool_or(kind = 'reply' AND jsonb_typeof(meta->'unverified') = 'array' AND jsonb_array_length(meta->'unverified') > 0) AS unverified,
         coalesce(sum((meta->>'input_tokens')::bigint + (meta->>'output_tokens')::bigint), 0) AS tokens,
         coalesce(sum((meta->>'cost_usd')::float), 0) AS cost
  FROM m GROUP BY 1, 2)
SELECT coalesce(jsonb_agg(row_to_json(t) ORDER BY t.model), '[]') FROM (
  SELECT model, count(*) AS questions,
         count(*) FILTER (WHERE status = 'answered') AS answered,
         count(*) FILTER (WHERE status = 'stopped') AS stopped,
         count(*) FILTER (WHERE status = 'error') AS errors,
         count(*) FILTER (WHERE status = 'refused') AS refused,
         round(avg((status = 'answered' AND unverified)::int) FILTER (WHERE status = 'answered')::numeric, 4) AS unverified_rate,
         round(avg(lookups)::numeric, 2) AS lookups_per_question,
         percentile_cont(0.5) WITHIN GROUP (ORDER BY tokens) AS tokens_p50,
         percentile_cont(0.99) WITHIN GROUP (ORDER BY tokens) AS tokens_p99,
         round(avg(cost)::numeric, 4) AS cost_usd_per_question
  FROM q WHERE model <> 'none' GROUP BY 1) t" > "$out.json"

{
  echo "## Assistant chat"
  echo
  echo "| Model | Questions | Answered | Stopped | Errors | Refused | Answers with unverified details | Lookups per question | Tokens p50 / p99 | Cost per question (USD) |"
  echo "|---|---|---|---|---|---|---|---|---|---|"
  jq -r '.[] | "| \(.model) | \(.questions) | \(.answered) | \(.stopped) | \(.errors) | \(.refused) | \((.unverified_rate // 0) * 100 | floor)% | \(.lookups_per_question) | \(.tokens_p50) / \(.tokens_p99) | \(.cost_usd_per_question) |"' "$out.json"
  echo
  echo "### Sample for human grading (correctness, usefulness, plain language: 1-5 each)"
  echo
  psql -c "SELECT '- **' || (q.content::jsonb->0->>'text') || '** ' || replace(left((a.content::jsonb->-1->>'text'), 400), E'\n', ' ')
           FROM investigation_messages q
           JOIN LATERAL (SELECT content FROM investigation_messages a WHERE a.investigation_id = q.investigation_id
                         AND a.seq > q.seq AND a.kind = 'reply' AND a.meta->>'stop_reason' <> 'tool_use'
                         AND a.seq < coalesce((SELECT min(seq) FROM investigation_messages n WHERE n.investigation_id = q.investigation_id
                                               AND n.kind = 'question' AND n.seq > q.seq), 2147483647)
                         ORDER BY seq LIMIT 1) a ON true
           WHERE q.kind = 'question' AND q.superseded_at IS NULL ORDER BY md5(q.investigation_id::text || q.seq) LIMIT 10"
} > "$out.md"
cat "$out.md"
