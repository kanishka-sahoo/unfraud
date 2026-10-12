"""Evaluating Ring Alerts and Revisions against the model at equal volume."""

from __future__ import annotations

from pathlib import Path

import polars as pl


def evaluate_rings(decisions_dir: Path, model_dir: Path, features_dir: Path, split: str) -> dict:
    """Ring flags and Revisions on Events the model approved, in one split
    ("val" or "test"), each compared with flagging the same number of the
    model's highest-scoring approved Events instead."""
    ids = pl.read_parquet(model_dir / f"{split}_predictions.parquet", columns=["event_id"])
    labels = pl.read_parquet(features_dir / "labels.parquet", columns=["event_id", "label"])
    d = pl.read_parquet(decisions_dir / "decisions.parquet").join(ids, on="event_id").join(labels, on="event_id")
    approved = d.filter(pl.col("model_action") == 0)
    rev = pl.read_parquet(decisions_dir / "revisions.parquet").join(approved.select("event_id", "label"), on="event_id")

    def baseline(n: int) -> dict:
        top = approved.sort("probability", descending=True).head(n)
        return {"alerts": n, "fraud": int(top["label"].sum()), "precision": float(top["label"].mean() or 0)}

    flagged = approved.filter(pl.col("reasons").str.contains("RING_MEMBER"))
    out = {
        "events": d.height,
        "fraud_missed_by_model": int(approved["label"].sum()),
        "ring_flags": {"alerts": flagged.height, "fraud": int(flagged["label"].sum()),
                       "precision": float(flagged["label"].mean() or 0)},
        "revisions": {"alerts": rev.height, "fraud": int(rev["label"].sum()),
                      "precision": float(rev["label"].mean() or 0)},
    }
    out["ring_flags_baseline"] = baseline(flagged.height)
    out["revisions_baseline"] = baseline(rev.height)
    union = approved.filter(pl.col("reasons").str.contains("RING_MEMBER") | pl.col("event_id").is_in(rev["event_id"]))
    out["either"] = {"alerts": union.height, "fraud": int(union["label"].sum()),
                     "precision": float(union["label"].mean() or 0)}
    out["either_baseline"] = baseline(union.height)
    # Fraud the rings catch that the model's equal-volume alternative would not.
    top = set(approved.sort("probability", descending=True).head(union.height)["event_id"].to_list())
    out["either"]["fraud_not_in_baseline"] = int(union.filter(~pl.col("event_id").is_in(list(top)) & (pl.col("label") == 1)).height)
    return out
