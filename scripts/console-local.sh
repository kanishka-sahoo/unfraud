#!/usr/bin/env bash
# Runs cases-api and the web app on this machine against the kind cluster,
# for development: scripts/console-local.sh start|stop. PIDs live in /tmp.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
run=/tmp/unfraud-console
mkdir -p "$run"
stop() {
  for f in "$run"/*.pid; do
    [ -e "$f" ] || continue
    kill "$(cat "$f")" 2>/dev/null || true
    rm -f "$f"
  done
}
start() {
  stop
  kubectl -n unfraud port-forward svc/cases-rw 15432:5432 >"$run/pg.log" 2>&1 & echo $! >"$run/pg.pid"
  kubectl -n unfraud port-forward deploy/graphsvc-tabformer 18086:8080 >"$run/graph.log" 2>&1 & echo $! >"$run/graph.pid"
  until nc -z 127.0.0.1 15432 2>/dev/null; do sleep 0.5; done
  pw=$(kubectl -n unfraud get secret cases-app -o jsonpath='{.data.password}' | base64 -d)
  PG_URL="postgres://unfraud:$pw@localhost:15432/unfraud" "$root/bin/casesapi" -brokers localhost:30092 -listen :18090 \
    -graph-url http://localhost:18086 >"$run/api.log" 2>&1 & echo $! >"$run/api.pid"
  (cd "$root/web" && CASES_API_URL=http://localhost:18090 PORT=3100 HOSTNAME=127.0.0.1 \
    "$HOME/.nvm/versions/node/v24.19.0/bin/node" .next/standalone/server.js >"$run/web.log" 2>&1 & echo $! >"$run/web.pid")
  until nc -z 127.0.0.1 3100 2>/dev/null && nc -z 127.0.0.1 18090 2>/dev/null; do sleep 0.5; done
  echo "console on http://localhost:3100"
}
"${1:-start}"
