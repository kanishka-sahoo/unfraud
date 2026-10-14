#!/usr/bin/env bash
# Downloads IBM TabFormer with the user's own Kaggle credentials, prepares
# tabformer/events.csv (unfraud-train prepare) and verifies both against
# data/checksums.sha256, so preparation is checked too. The data is never
# committed.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
raw="$root/data/raw"
sums="$root/data/checksums.sha256"
dir="$raw/tabformer"

command -v kaggle >/dev/null || { echo "kaggle CLI not found (uv tool install kaggle)" >&2; exit 1; }

mkdir -p "$dir"
for f in credit_card_transactions-ibm_v2.csv sd254_cards.csv sd254_users.csv; do
  [[ -f "$dir/$f" ]] && continue
  kaggle datasets download ealtman2019/credit-card-transactions -f "$f" -p "$dir" -q
  if [[ -f "$dir/$f.zip" ]]; then unzip -oq "$dir/$f.zip" -d "$dir" && rm "$dir/$f.zip"; fi
done
[[ -f "$dir/events.csv" ]] || uv run --project "$root/train" unfraud-train prepare --raw "$raw"

cd "$raw"
if [[ "${1:-}" == "--record" ]]; then
  find tabformer -name '*.csv' | sort | sed 's#^#./#' | xargs sha256sum > "$sums"
  echo "recorded checksums to $sums"
else
  sha256sum --quiet -c "$sums" && echo "dataset verified"
fi
