#!/usr/bin/env bash
# Summarises the Summaries in Postgres per model: Grounding pass rate,
# attempts, latency and cost, plus a random sample for human grading.
#   scripts/summary-report.sh results/summaries
set -euo pipefail
out=${1:-results/summaries}
psql() { kubectl -n unfraud exec cases-1 -c postgres -- psql -U postgres -d unfraud -Atq "$@"; }

psql -c "
SELECT coalesce(jsonb_agg(row_to_json(t) ORDER BY t.model), '[]') FROM (
  SELECT summary->'meta'->>'driver' AS driver,
         coalesce(summary->'meta'->>'model', 'none') AS model,
         count(*) AS cases,
         count(*) FILTER (WHERE summary_status = 'done') AS grounded,
         round(avg((summary_status = 'done')::int)::numeric, 4) AS grounded_rate,
         count(*) FILTER (WHERE (summary->>'attempts')::int = 2 AND summary_status = 'done') AS grounded_on_retry,
         count(*) FILTER (WHERE summary->>'error' IS NOT NULL) AS errors,
         percentile_cont(0.5) WITHIN GROUP (ORDER BY (summary->'meta'->>'latency_ms')::float) AS latency_p50_ms,
         percentile_cont(0.99) WITHIN GROUP (ORDER BY (summary->'meta'->>'latency_ms')::float) AS latency_p99_ms,
         round((1000 * avg((summary->'meta'->>'cost_usd')::float))::numeric, 4) AS cost_usd_per_1k_cases
  FROM cases WHERE summary_status IN ('done', 'failed') GROUP BY 1, 2) t" > "$out.json"

{
  echo "## Case Summaries"
  echo
  echo "| Driver | Model | Cases | Grounded | Grounded on retry | Errors | Latency p50 / p99 (ms) | Cost per 1k Cases (USD) |"
  echo "|---|---|---|---|---|---|---|---|"
  jq -r '.[] | "| \(.driver) | \(.model) | \(.cases) | \(.grounded_rate * 100 | floor)% | \(.grounded_on_retry) | \(.errors) | \(.latency_p50_ms) / \(.latency_p99_ms) | \(.cost_usd_per_1k_cases) |"' "$out.json"
  echo
  echo "### Sample for human grading (accuracy, usefulness, brevity: 1-5 each)"
  echo
  psql -c "SELECT '- **' || profile || '/' || event_id || '** (' || (summary->'meta'->>'model') || '): ' ||
                  replace(summary->>'summary', E'\n', ' ')
           FROM cases WHERE summary_status = 'done' ORDER BY md5(event_id) LIMIT 10"
} > "$out.md"
cat "$out.md"
