"""unfraud-train: dataset preparation, offline training and evaluation."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser(prog="unfraud-train")
    sub = p.add_subparsers(dest="cmd", required=True)

    pr = sub.add_parser("prepare", help="normalize a raw dataset into <raw>/<profile>/events.csv")
    pr.add_argument("--raw", type=Path, default=Path("data/raw"))
    pr.add_argument("profile", choices=["tabformer", "sparkov", "amlworld"])

    t = sub.add_parser("train", help="train LightGBM on one or more Accuracy Runs' tables")
    t.add_argument("--features", type=Path, nargs="+", required=True,
                   help="featurize output folder; several pool their Events (core feature set only)")
    t.add_argument("--out", type=Path, required=True, help="model output folder")
    t.add_argument("--feature-set", default="realtime", help="core, static, realtime or graph")
    t.add_argument("--budget", type=float, default=0.005, help="Alert Budget (share of Events flagged)")
    t.add_argument("--max-rounds", type=int, default=1000,
                   help="boosting rounds before stopping even if validation still improves")
    t.add_argument("--param", action="append", default=[], metavar="KEY=VALUE",
                   help="override a LightGBM parameter, e.g. --param max_depth=10")

    tr = sub.add_parser("transfer", help="score another Profile's test period with a core model, unchanged")
    tr.add_argument("--model", type=Path, required=True)
    tr.add_argument("--features", type=Path, required=True)
    tr.add_argument("--out", type=Path, required=True, help="JSON output")

    r = sub.add_parser("report", help="collect a Profile's trained models into results/")
    r.add_argument("--models", type=Path, required=True, help="the Profile's model folder")
    r.add_argument("--out", type=Path, required=True, help="results path without extension")
    r.add_argument("--features", type=Path, help="featurize output, to evaluate the scorer's decisions")

    c = sub.add_parser("report-pooled", help="collect core, pooled and transfer results into results/")
    c.add_argument("--models", type=Path, default=Path("models"))
    c.add_argument("--out", type=Path, required=True, help="results path without extension")

    f = sub.add_parser("fixture", help="write Go evaluator test fixtures")
    f.add_argument("--out", type=Path, required=True)

    args = p.parse_args()
    if args.cmd == "prepare":
        from .prepare import prepare

        print(prepare(args.raw, args.profile))
    elif args.cmd == "train":
        from .train import train

        overrides = {}
        for kv in args.param:
            k, v = kv.split("=", 1)
            overrides[k] = json.loads(v) if v[:1].isdigit() or v[:1] == "-" else v
        report = train(args.features, args.out, args.feature_set, args.budget, args.max_rounds, overrides)
        print(json.dumps({"feature_set": report["feature_set"], "best_iteration": report["best_iteration"],
                          "test": {s: r["test"] for s, r in report["per_source"].items()}}, indent=2))
    elif args.cmd == "transfer":
        from .train import transfer

        result = transfer(args.model, args.features)
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps(result["test"], indent=2))
    elif args.cmd == "report":
        from .report import report

        print(report(args.models, args.out, args.features))
    elif args.cmd == "report-pooled":
        from .report import report_pooled

        print(report_pooled(args.models, args.out))
    elif args.cmd == "fixture":
        from .fixture import write

        write(args.out)
