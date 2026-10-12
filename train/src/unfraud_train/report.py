"""Collecting model manifests into the published results table."""

from __future__ import annotations

import json
import platform
from pathlib import Path

ROWS = [("static", "Model, static features"), ("realtime", "+ real-time features")]


def report(models: Path, out: Path) -> str:
    runs = {}
    for name, _ in ROWS:
        manifest = models / f"lgbm-{name}" / "manifest.json"
        if manifest.exists():
            runs[name] = json.loads(manifest.read_text())
    if not runs:
        raise SystemExit(f"no models under {models}")
    first = next(iter(runs.values()))
    budgets = list(first["test_budgets"])
    main = "0.05"

    lines = [
        f"## {first['profile']}: LightGBM, time split (test = last month)",
        "",
        "Thresholds for each Alert Budget are set on the validation month and applied unchanged to the test month. "
        f"Label delay {first['label_delay']}.",
        "",
        "| Configuration | PR-AUC | ROC-AUC | " + " | ".join(f"Recall @ {float(b):.1%}" for b in budgets)
        + f" | $-recall @ {float(main):.0%} | Precision @ {float(main):.0%} | Trees |",
        "|---|---|---|" + "---|" * len(budgets) + "---|---|---|",
    ]
    for name, label in ROWS:
        r = runs.get(name)
        if not r:
            continue
        t, tb = r["test"], r["test_budgets"]
        lines.append(
            f"| {label} | {t['pr_auc']:.4f} | {t['roc_auc']:.4f} | "
            + " | ".join(f"{tb[b]['recall']:.1%}" for b in budgets)
            + f" | {tb[main].get('dollar_recall', float('nan')):.1%} | {tb[main]['precision']:.1%} | {r['best_iteration']} |"
        )
    ceilings = ", ".join(f"{float(b):.1%} → {first['test_budgets'][b]['recall_ceiling']:.0%}" for b in budgets)
    lines += ["", f"Recall ceilings (fraud a perfect model could catch at each budget): {ceilings}."]
    split = first["split"]
    lines += [
        "",
        "Split: " + ", ".join(f"{s} {v['events']:,} Events ({v['fraud'] / v['events']:.2%} fraud)" for s, v in split.items()) + ".",
        f"Features table sha256 `{first['features_sha256'][:16]}…`, LightGBM {first['lightgbm_version']}, "
        f"featurize at `{first['featurize_revision'][:12]}`.",
    ]
    table = "\n".join(lines) + "\n"

    out.parent.mkdir(parents=True, exist_ok=True)
    summary = {
        "profile": first["profile"],
        "machine": {"system": platform.system(), "machine": platform.machine(), "python": platform.python_version()},
        "runs": {k: {key: v[key] for key in ("feature_set", "best_iteration", "alert_budget", "threshold",
                                              "label_delay", "split", "validation", "test", "test_budgets", "features_sha256",
                                              "featurize_revision", "train_revision", "lightgbm_version",
                                              "params")}
                 for k, v in runs.items()},
    }
    out.with_suffix(".json").write_text(json.dumps(summary, indent=2) + "\n")
    out.with_suffix(".md").write_text(table)
    return table
