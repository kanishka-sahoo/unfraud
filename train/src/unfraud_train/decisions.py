"""Evaluating the scorer's Decisions (rules, model, combined) on the test month."""

from __future__ import annotations

import json
from pathlib import Path

import numpy as np
import polars as pl

ACTIONS = {"rules": "rule_action", "model": "model_action", "combined": "action"}


def evaluate_decisions(model_dir: Path, features_dir: Path) -> dict:
    manifest = json.loads((model_dir / "manifest.json").read_text())
    test_ids = pl.read_parquet(model_dir / "test_predictions.parquet", columns=["event_id"])
    labels = pl.read_parquet(features_dir / "labels.parquet", columns=["event_id", "label"])
    amounts = pl.read_parquet(features_dir / "features.parquet", columns=["event_id", "amount_usd"])
    d = (
        pl.read_parquet(model_dir / "decisions.parquet")
        .join(test_ids, on="event_id")
        .join(labels, on="event_id")
        .join(amounts, on="event_id")
    )
    fraud = pl.col("label") == 1
    total_fraud = d.filter(fraud).height
    total_amount = d.filter(fraud)["amount_usd"].sum()

    def score(flag: pl.Expr) -> dict:
        f = d.filter(flag)
        tp = f.filter(fraud)
        return {
            "alert_rate": f.height / d.height,
            "precision": tp.height / max(f.height, 1),
            "recall": tp.height / total_fraud,
            "dollar_recall": tp["amount_usd"].sum() / total_amount,
            "flagged": f.height,
        }

    out = {name: score(pl.col(col) > 0) for name, col in ACTIONS.items()}

    # The fair comparison for Rules: the model alone, flagging as many Events
    # as model + Rules do, with its threshold set on the validation month.
    val = pl.read_parquet(model_dir / "val_predictions.parquet")["score"].to_numpy()
    threshold = float(np.quantile(val, 1 - out["combined"]["alert_rate"]))
    test = pl.read_parquet(model_dir / "test_predictions.parquet")
    flagged_ids = test.filter(pl.col("score") > threshold).select("event_id").with_columns(pl.lit(True).alias("eq"))
    d = d.join(flagged_ids, on="event_id", how="left").with_columns(pl.col("eq").fill_null(False))
    out["model_equal_volume"] = score(pl.col("eq"))
    out["blocks"] = score(pl.col("action") == 2)
    per_rule = {}
    hits = d.with_columns(pl.col("reasons").str.split(",")).explode("reasons").filter(pl.col("reasons") != "")
    for code, g in hits.group_by("reasons"):
        per_rule[code[0]] = {"hits": g.height, "precision": g.filter(fraud).height / g.height,
                             "fraud_caught": g.filter(fraud).height}
    out["per_rule"] = dict(sorted(per_rule.items()))
    out["events"] = d.height
    out["thresholds"] = {"block_budget": "0.005", "review_budget": "0.02",
                         "block": manifest["test_budgets"]["0.005"]["threshold"],
                         "review": manifest["test_budgets"]["0.02"]["threshold"]}
    return out
