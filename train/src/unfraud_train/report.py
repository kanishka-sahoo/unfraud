"""Collecting model manifests into the published results table."""

from __future__ import annotations

import json
import platform
from pathlib import Path

ROWS = [
    ("static", "Model, static features"),
    ("realtime", "+ real-time features"),
    ("graph", "+ graph features"),
    ("realtime-uncapped", "+ real-time features, unlimited depth (reference, not served)"),
]


def decisions_section(models: Path, features: Path | None) -> tuple[list[str], dict | None]:
    served = models / "lgbm-realtime"
    if features is None or not (served / "decisions.parquet").exists():
        return [], None
    from .decisions import evaluate_decisions

    ev = evaluate_decisions(served, features)
    th = ev["thresholds"]
    lines = [
        "",
        "### Decisions (scorer Accuracy Run, served model)",
        "",
        f"The scorer blocks above the {float(th['block_budget']):.1%}-budget threshold ({th['block']:.4f}) and sends to "
        f"review above the {float(th['review_budget']):.0%}-budget threshold ({th['review']:.4f}); "
        "Rules can only escalate.",
        "",
        "| Decision source | Alert rate | Precision | Recall | $-recall |",
        "|---|---|---|---|---|",
    ]
    names = {"rules": "Rules only", "model": "Model only (review + block)", "combined": "Model + Rules",
             "model_equal_volume": "Model only, at the same alert volume as Model + Rules",
             "blocks": "Blocks only (model + rules)"}
    for k, label in names.items():
        r = ev[k]
        lines.append(f"| {label} | {r['alert_rate']:.2%} | {r['precision']:.1%} | {r['recall']:.1%} | {r['dollar_recall']:.1%} |")
    lines += ["", "| Rule | Hits | Precision | Fraud caught |", "|---|---|---|---|"]
    for code, r in ev["per_rule"].items():
        lines.append(f"| `{code}` | {r['hits']:,} | {r['precision']:.1%} | {r['fraud_caught']:,} |")
    return lines, ev


def system_run_section(out: Path) -> list[str]:
    prefix = out.name.split("-lightgbm")[0]
    runs = {}
    for variant, label in (("aligned", "Aligned (ADR-0016)"), ("unaligned", "Unaligned (control)")):
        p = out.parent / f"{out.name.split('-lightgbm')[0]}-system-run-{variant}.json"
        if p.exists():
            runs[label] = json.loads(p.read_text())
    if not runs:
        return []
    lines = [
        "",
        "### System Run (in-cluster scorer, lite profile, 4-core / 8 GB machine)",
        "",
        "One scorer pod scoring the whole input topic from a backlog, with features in Valkey, compared Event by Event "
        "with the Accuracy Run (`scripts/system-run.sh`, `cmd/drift`). End-to-end latency is not reported here: "
        "a backlog measures queueing, not scoring; it needs a live-paced replay.",
        "",
        "| Scorer | Events/s | Probabilities differing | Mean abs diff | p99 abs diff | Max abs diff | Model decisions differing |",
        "|---|---|---|---|---|---|---|",
    ]
    for label, r in runs.items():
        lines.append(
            f"| {label} | {r.get('events_per_second', 0):,} | {r['probability_differs'] / r['compared']:.2%} | "
            f"{r['mean_abs_probability_diff']:.2e} | {r['p99_abs_probability_diff']:.2e} | "
            f"{r['max_abs_probability_diff']:.3f} | {r['action_differs']:,} of {r['compared']:,} |"
        )
    final = {stage: out.parent / f"{prefix}-system-run-final-{stage}.json" for stage in ("scorer", "graphsvc")}
    if all(f.exists() for f in final.values()):
        sc, gs = (json.loads(f.read_text()) for f in final.values())
        lines += [
            "",
            "Full pipeline in the cluster (scorer with a Shadow Model, graph-svc, case-svc, summarizer, Prometheus and "
            "Grafana all running, about 3.7 GB):",
            "",
            "| Stage | Decisions compared | Probabilities differing | Decisions differing |",
            "|---|---|---|---|",
            f"| Provisional (scorer, aligned) | {sc['compared']:,} | {sc['probability_differs'] / sc['compared']:.2%} | {sc['action_differs']:,} |",
            f"| Revised (graph-svc, single writer) | {gs['compared']:,} | {gs['probability_differs'] / gs['compared']:.2%} | {gs['action_differs']:,} |",
        ]
    lat = {stage: out.parent / f"{prefix}-latency-{stage}.json" for stage in ("scored", "revised")}
    if all(f.exists() for f in lat.values()):
        lines += [
            "",
            "### End-to-end latency (live-paced replay)",
            "",
            "The first 60,000 IEEE-CIS Events replayed at about 200 a second, timed from the Event entering Kafka to its "
            "Decision being produced, with the full pipeline running and the scorer also evaluating its Shadow Model. "
            "Scoring itself takes 3.6 ms at p50 and 11 ms at p95; the rest of the scorer's tail is time records wait for "
            "Event Time alignment across partitions (with alignment off, p95 was 142 ms but Decision drift is 16 times "
            "higher, ADR-0016). Expiry sweeps run in the background in 100-key batches so they never stall scoring.",
            "",
            "| Stage | Decisions | p50 | p95 | p99 | max |",
            "|---|---|---|---|---|---|",
        ]
        for stage, label in (("scored", "Provisional (scorer)"), ("revised", "Revised (graph-svc)")):
            r = json.loads(lat[stage].read_text())
            lines.append(f"| {label} | {r['compared']:,} | {r['end_to_end_latency_p50_ms']} ms | {r['end_to_end_latency_p95_ms']} ms | "
                         f"{r['end_to_end_latency_p99_ms']} ms | {r['end_to_end_latency_max_ms']} ms |")
    shadow = out.parent / f"{prefix}-shadow.json"
    if shadow.exists():
        sh = json.loads(shadow.read_text())
        total = sum(sh["counts"].values())
        agree = sum(v for k, v in sh["counts"].items() if k.split("/")[0] == k.split("/")[1])
        lines += ["", "### Shadow Model", "",
                  f"{sh['shadow']} scored every Event beside the served model ({sh['active']}). They chose the same "
                  f"action for {agree / total:.2%} of {total:,} Events.", "",
                  "| Served → Shadow | Events |", "|---|---|"]
        for k, v in sorted(sh["counts"].items(), key=lambda kv: -kv[1]):
            a, b = k.split("/")
            if a != b:
                lines.append(f"| {a} → {b} | {v:,} |")
    return lines


def rings_section(models: Path, features: Path | None) -> list[str]:
    rings_dir, model_dir = models / "rings-3_2_0", models / "lgbm-graph"
    if features is None or not (rings_dir / "revisions.parquet").exists():
        return []
    from .rings import evaluate_rings

    lines = [
        "",
        "### Ring Alerts (rejected on the validation month)",
        "",
        "Policy: a Louvain Community with at least 3 cards, at least 2 of them with released fraud Labels. "
        "Ring cards' later Events are flagged (`RING_MEMBER`) and their Events approved in the previous 7 days are "
        "revised. Only Events the graph model approved count. Each row is compared with flagging the same number of "
        "the model's highest-scoring approved Events instead.",
        "",
        "| Month | Mechanism | Alerts | Fraud | Precision | Model, same volume: fraud | Precision |",
        "|---|---|---|---|---|---|---|",
    ]
    for split, label in (("val", "Validation"), ("test", "Test")):
        r = evaluate_rings(rings_dir, model_dir, features, split)
        for key, name in (("ring_flags", "Ring flags"), ("revisions", "Revisions"), ("either", "Either")):
            a, b = r[key], r[key + "_baseline"]
            lines.append(f"| {label} | {name} | {a['alerts']:,} | {a['fraud']:,} | {a['precision']:.1%} | "
                         f"{b['fraud']:,} | {b['precision']:.1%} |")
    lines += ["", "Stricter policies (fraud share ≥ 30% or 50%, or ≥ 3 fraud cards) produced at most 7 validation "
              "alerts and no fraud. Ring Alerts are therefore off for IEEE-CIS; the graph contributes through its "
              "features."]
    return lines


def report(models: Path, out: Path, features: Path | None = None) -> str:
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
    extra, decisions = decisions_section(models, features)
    lines += extra
    lines += rings_section(models, features)
    lines += system_run_section(out)
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
        "decisions": decisions,
    }
    out.with_suffix(".json").write_text(json.dumps(summary, indent=2) + "\n")
    out.with_suffix(".md").write_text(table)
    return table
