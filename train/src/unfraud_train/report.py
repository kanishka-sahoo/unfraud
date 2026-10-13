"""Collecting model manifests into the published results table."""

from __future__ import annotations

import json
import platform
from pathlib import Path

ROWS = [
    ("static", "Model, raw features (core + dataset extras)"),
    ("realtime", "+ real-time features"),
    ("graph", "+ graph features"),
    ("core", "Core features only (shared by every payment Profile)"),
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
        f"The scorer blocks above the {float(th['block_budget']):.1%}-budget threshold ({th['block']:.4g}) and sends to "
        f"review above the {float(th['review_budget']):.0%}-budget threshold ({th['review']:.4g}); "
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
    if not ev["per_rule"]:
        lines += ["", "Every Rule is retired on this Profile: none was as precise on the validation period as the model "
                  "at the review budget (`internal/profile/payments`)."]
    else:
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
            "Events replayed at a steady rate, timed from the Event entering Kafka to its Decision being produced, "
            "with the full pipeline running.",
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
        "### Ring Alerts",
        "",
        "Policy: a Louvain Community with at least 3 Nodes, at least 2 of them with released fraud Labels. "
        "Ring Nodes' later Events are flagged (`RING_MEMBER`) and their Events approved in the previous 7 days are "
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
    main = "0.005"

    lines = [
        f"## {first['profile']}: LightGBM, time split",
        "",
        "Thresholds for each Alert Budget are set on the validation period and applied unchanged to the test period, "
        "so the share of test Events flagged can drift from the budget; the alert-rate column shows by how much. "
        f"Label delay {next(iter(first['label_delay'].values()))}.",
        "",
        "| Configuration | PR-AUC | ROC-AUC | " + " | ".join(f"Recall @ {float(b):.1%}" for b in budgets)
        + f" | Test alert rate @ {float(main):.1%} | $-recall @ {float(main):.1%} | Precision @ {float(main):.1%} | Trees |",
        "|---|---|---|" + "---|" * len(budgets) + "---|---|---|---|",
    ]
    for name, label in ROWS:
        r = runs.get(name)
        if not r:
            continue
        t, tb = r["test"], r["test_budgets"]
        lines.append(
            f"| {label} | {t['pr_auc']:.4f} | {t['roc_auc']:.4f} | "
            + " | ".join(f"{tb[b]['recall']:.1%}" for b in budgets)
            + f" | {tb[main]['alert_rate']:.2%} | {tb[main]['dollar_recall']:.1%} | {tb[main]['precision']:.1%} | {r['best_iteration']} |"
        )
    ceilings = ", ".join(f"{float(b):.1%} → {first['test_budgets'][b]['recall_ceiling']:.0%}" for b in budgets)
    lines += ["", f"Recall ceilings (fraud a perfect model could catch at each budget): {ceilings}."]
    split = first["split"]
    lines += [
        "",
        "Split: " + ", ".join(f"{s} {v['events']:,} Events ({v['fraud'] / v['events']:.2%} fraud)" for s, v in split.items()) + ".",
        f"Features table sha256 `{next(iter(first['features_sha256'].values()))[:16]}…`, "
        f"LightGBM {first['lightgbm_version']}, featurize at `{next(iter(first['featurize_revision'].values()))[:12]}`.",
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


# Core models: one per Profile (models/<profile>/lgbm-core) and pooled ones
# (models/pooled/<name>). Transfer results score a Profile a model never saw
# (models/pooled/transfer/<model>__<profile>.json).
POOLED = [
    ("tabformer", "tabformer/lgbm-core"),
    ("sparkov", "sparkov/lgbm-core"),
    ("amlworld", "amlworld/lgbm-core"),
    ("tabformer + sparkov", "pooled/cards"),
    ("all three", "pooled/all"),
]
TARGETS = ["tabformer", "sparkov", "amlworld"]


def report_pooled(models: Path, out: Path) -> str:
    budget = "0.005"
    cells: dict[str, dict[str, dict | None]] = {}
    for label, rel in POOLED:
        mpath = models / rel / "manifest.json"
        if not mpath.exists():
            continue
        m = json.loads(mpath.read_text())
        row = {}
        for target in TARGETS:
            if target in m["per_source"]:
                row[target] = m["per_source"][target] | {"seen": True}
            else:
                t = models / "pooled" / "transfer" / f"{rel.replace('/', '_')}__{target}.json"
                row[target] = json.loads(t.read_text()) | {"seen": False} if t.exists() else None
        cells[label] = row
    if not cells:
        raise SystemExit(f"no core models under {models}")

    def cell(r: dict | None) -> str:
        if r is None:
            return "–"
        s = f"{r['test']['pr_auc']:.3f} / {r['test_budgets'][budget]['recall']:.0%}"
        return s if r["seen"] else f"*{s}*"

    lines = [
        "## Core features: one model across datasets",
        "",
        "Every payment Profile maps its dataset onto the same core features (amount in USD, channel, merchant "
        "category, hour, weekday) and computes the same real-time features (payer and payee velocity, unusual amounts, "
        "new payees, released fraud Labels). A model trained on them binds to any payment Profile.",
        "",
        f"Each cell is test PR-AUC / recall at a {float(budget):.1%} Alert Budget. Italic cells are datasets the model "
        "never saw in training, scored unchanged; thresholds always come from the scored dataset's own validation "
        "period. Pooled models train on every listed dataset's training period at once.",
        "",
        "| Trained on | " + " | ".join(TARGETS) + " |",
        "|---|" + "---|" * len(TARGETS),
    ]
    for label, row in cells.items():
        lines.append(f"| {label} | " + " | ".join(cell(row[t]) for t in TARGETS) + " |")
    base = {t: next((r[t]["test"] for r in cells.values() if r.get(t)), None) for t in TARGETS}
    lines.append("| *(guessing: the test fraud rate)* | "
                 + " | ".join(f"{b['fraud'] / b['events']:.3f} / 0.5%" if b else "–" for b in base.values()) + " |")
    table = "\n".join(lines) + "\n"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.with_suffix(".json").write_text(json.dumps(cells, indent=2) + "\n")
    out.with_suffix(".md").write_text(table)
    return table
