#!/usr/bin/env bash
# Downloads the public datasets with the user's own Kaggle credentials and
# verifies them against data/checksums.sha256. Kaggle licences forbid
# redistribution, so the data itself is never committed.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
raw="$root/data/raw"
sums="$root/data/checksums.sha256"

command -v kaggle >/dev/null || { echo "kaggle CLI not found (uv tool install kaggle)" >&2; exit 1; }

fetch_competition() { # dir competition file...
  local dir="$raw/$1" comp="$2"; shift 2
  mkdir -p "$dir"
  for f in "$@"; do
    [[ -f "$dir/$f" ]] && continue
    if ! kaggle competitions download -c "$comp" -f "$f" -p "$dir" -q; then
      echo "error: cannot download $comp. Accept the rules at https://www.kaggle.com/competitions/$comp/rules" >&2
      echo "       (Kaggle also requires a phone-verified account for competition data)" >&2
      return 1
    fi
    if [[ -f "$dir/$f.zip" ]]; then unzip -oq "$dir/$f.zip" -d "$dir" && rm "$dir/$f.zip"; fi
  done
}

fetch_dataset() { # dir dataset expected-file
  local dir="$raw/$1"
  mkdir -p "$dir"
  [[ -f "$dir/$3" ]] && return
  kaggle datasets download "$2" -p "$dir" --unzip -q
}

failed=0
# IEEE-CIS test labels are private, so only the training files are useful.
fetch_competition ieee-cis ieee-fraud-detection train_transaction.csv train_identity.csv || failed=1
fetch_dataset paysim ealaxi/paysim1 PS_20174392719_1491204439457_log.csv || failed=1
fetch_dataset elliptic ellipticco/elliptic-data-set elliptic_bitcoin_dataset/elliptic_txs_features.csv || failed=1

cd "$raw"
if [[ "${1:-}" == "--record" ]]; then
  find . -name '*.csv' | sort | xargs sha256sum > "$sums"
  echo "recorded checksums to $sums"
else
  sha256sum --quiet -c "$sums" && echo "all present datasets verified"
fi
exit $failed
