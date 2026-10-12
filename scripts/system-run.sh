#!/usr/bin/env bash
# System Run: score a Profile's whole input topic with the in-cluster scorer
# from clean state, then compare its Decisions with the Accuracy Run's.
#   scripts/system-run.sh <profile> <accuracy decisions.parquet> <out.json>
set -euo pipefail
profile=$1 decisions=$2 out=$3
ns=unfraud chart=deploy/helm/unfraud
kafka() { kubectl -n "$ns" exec unfraud-dual-role-0 -c kafka -- "$@"; }
count() { kafka bin/kafka-get-offsets.sh --bootstrap-server localhost:9092 --topic "$1" 2>/dev/null | awk -F: '{s+=$3} END{print s+0}'; }

echo "stopping scorer"
helm upgrade unfraud "$chart" -n "$ns" --reuse-values --set scorer.replicas=0 >/dev/null
until [ -z "$(kubectl -n "$ns" get pods -l app.kubernetes.io/name=scorer,unfraud.dev/profile="$profile" -o name)" ]; do sleep 2; done
# Delete the consumer group so the scorer starts from the earliest offset.
# kafka-consumer-groups exits 0 even when it refuses (while the departed
# member's session is still alive), so check that the group is really gone.
while kafka bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --list 2>/dev/null | grep -qx "scorer.$profile"; do
  kafka bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --delete --group "scorer.$profile" >/dev/null 2>&1 || true
  sleep 5
done

echo "resetting scored.$profile and feature state"
kubectl -n "$ns" delete kafkatopic "scored.$profile" --wait >/dev/null
kubectl -n "$ns" exec valkey-0 -- valkey-cli flushall >/dev/null
helm upgrade unfraud "$chart" -n "$ns" --reuse-values --set scorer.replicas=1 >/dev/null
kubectl -n "$ns" wait "kafkatopic/scored.$profile" --for=condition=Ready --timeout=120s >/dev/null
kubectl -n "$ns" rollout status "deploy/scorer-$profile" --timeout=180s >/dev/null

# Events = input records that are Events; the scorer writes one Decision each.
want=$(go run ./cmd/drift -profile "$profile" -decisions "$decisions" -count-only)
start=$(date +%s)
echo "scoring $want events"
until [ "$(count "scored.$profile")" -ge "$want" ]; do
  sleep 10
  if [ $(( $(date +%s) - start )) -gt 120 ] && [ "$(count "scored.$profile")" -eq 0 ]; then
    echo "nothing scored after 2 minutes; is the scorer consuming?" >&2
    exit 1
  fi
done
elapsed=$(( $(date +%s) - start ))
echo "scored in ${elapsed}s ($(( want / elapsed )) events/s)"
go run ./cmd/drift -profile "$profile" -decisions "$decisions" -elapsed "$elapsed" | tee "$out"
