#!/usr/bin/env bash
# Downloads the public datasets with the user's own Kaggle credentials,
# verifies them against data/checksums.sha256, and normalizes each payment
# dataset into <profile>/events.csv (unfraud-train prepare). The prepared
# files' checksums are recorded too, so preparation is checked as well.
# Licences differ, so the data itself is never committed.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
raw="$root/data/raw"
sums="$root/data/checksums.sha256"

command -v kaggle >/dev/null || { echo "kaggle CLI not found (uv tool install kaggle)" >&2; exit 1; }

fetch() { # dir dataset file...
  local dir="$raw/$1" ds="$2"; shift 2
  mkdir -p "$dir"
  for f in "$@"; do
    [[ -f "$dir/$f" ]] && continue
    kaggle datasets download "$ds" -f "$f" -p "$dir" -q
    if [[ -f "$dir/$f.zip" ]]; then unzip -oq "$dir/$f.zip" -d "$dir" && rm "$dir/$f.zip"; fi
  done
}

fetch tabformer ealtman2019/credit-card-transactions credit_card_transactions-ibm_v2.csv sd254_cards.csv sd254_users.csv
fetch sparkov kartik2112/fraud-detection fraudTrain.csv fraudTest.csv
fetch amlworld ealtman2019/ibm-transactions-for-anti-money-laundering-aml HI-Small_Trans.csv HI-Small_accounts.csv
fetch paysim ealaxi/paysim1 PS_20174392719_1491204439457_log.csv

cd "$raw"
for p in tabformer sparkov amlworld; do
  [[ -f "$p/events.csv" ]] || uv run --project "$root/train" unfraud-train prepare --raw "$raw" "$p"
done

if [[ "${1:-}" == "--record" ]]; then
  find . -name '*.csv' | sort | xargs sha256sum > "$sums"
  echo "recorded checksums to $sums"
else
  sha256sum --quiet -c "$sums" && echo "all datasets verified"
fi
