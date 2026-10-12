"""unfraud-train: offline training and evaluation."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser(prog="unfraud-train")
    sub = p.add_subparsers(dest="cmd", required=True)

    t = sub.add_parser("train", help="train LightGBM on an Accuracy Run's tables")
    t.add_argument("--features", type=Path, required=True, help="featurize output folder")
    t.add_argument("--out", type=Path, required=True, help="model output folder")
    t.add_argument("--feature-set", choices=["static", "realtime"], default="realtime")
    t.add_argument("--budget", type=float, default=0.005, help="Alert Budget (share of Events flagged)")
    t.add_argument("--max-rounds", type=int, default=3000)

    r = sub.add_parser("report", help="collect trained models into results/")
    r.add_argument("--models", type=Path, required=True, help="folder with lgbm-static and lgbm-realtime")
    r.add_argument("--out", type=Path, required=True, help="results path without extension")

    f = sub.add_parser("fixture", help="write Go evaluator test fixtures")
    f.add_argument("--out", type=Path, required=True)

    args = p.parse_args()
    if args.cmd == "train":
        from .train import train

        report = train(args.features, args.out, args.feature_set, args.budget, args.max_rounds)
        print(json.dumps({k: report[k] for k in ("feature_set", "best_iteration", "test")}, indent=2))
    elif args.cmd == "report":
        from .report import report

        print(report(args.models, args.out))
    elif args.cmd == "fixture":
        from .fixture import write

        write(args.out)
