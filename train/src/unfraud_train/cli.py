"""unfraud-train: dataset preparation, offline training and evaluation."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser(prog="unfraud-train")
    sub = p.add_subparsers(dest="cmd", required=True)

    pr = sub.add_parser("prepare", help="write <raw>/tabformer/events.csv from the raw TabFormer files")
    pr.add_argument("--raw", type=Path, default=Path("data/raw"))

    t = sub.add_parser("train", help="train LightGBM on an Accuracy Run's tables")
    t.add_argument("--features", type=Path, required=True, help="featurize output folder")
    t.add_argument("--out", type=Path, required=True, help="model output folder")
    t.add_argument("--feature-set", default="realtime", help="static, realtime or graph")
    t.add_argument("--budget", type=float, default=0.005, help="Alert Budget (share of Events flagged)")
    t.add_argument("--max-rounds", type=int, default=1000,
                   help="boosting rounds before stopping even if validation still improves")
    t.add_argument("--param", action="append", default=[], metavar="KEY=VALUE",
                   help="override a LightGBM parameter, e.g. --param max_depth=10")

    r = sub.add_parser("report", help="collect a Profile's trained models into results/")
    r.add_argument("--models", type=Path, required=True, help="the Profile's model folder")
    r.add_argument("--out", type=Path, required=True, help="results path without extension")
    r.add_argument("--features", type=Path, help="featurize output, to evaluate the scorer's decisions")

    f = sub.add_parser("fixture", help="write Go evaluator test fixtures")
    f.add_argument("--out", type=Path, required=True)

    args = p.parse_args()
    if args.cmd == "prepare":
        from .prepare import prepare

        print(prepare(args.raw))
    elif args.cmd == "train":
        from .train import train

        overrides = {}
        for kv in args.param:
            k, v = kv.split("=", 1)
            overrides[k] = json.loads(v) if v[:1].isdigit() or v[:1] == "-" else v
        report = train(args.features, args.out, args.feature_set, args.budget, args.max_rounds, overrides)
        print(json.dumps({k: report[k] for k in ("feature_set", "best_iteration", "test")}, indent=2))
    elif args.cmd == "report":
        from .report import report

        print(report(args.models, args.out, args.features))
    elif args.cmd == "fixture":
        from .fixture import write

        write(args.out)
