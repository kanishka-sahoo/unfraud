"""Training a LightGBM model on one or more Accuracy Runs' tables."""

from __future__ import annotations

import json
import subprocess
import time
from pathlib import Path

import lightgbm as lgb
import numpy as np
import polars as pl

from .data import prepare
from .metrics import at_budgets, evaluate, threshold_for_budget

PARAMS = {
    "objective": "binary",
    "learning_rate": 0.1,
    "num_leaves": 127,
    # Depth 10 keeps trees shallow enough for fast scoring and TreeSHAP in Go.
    "max_depth": 10,
    "min_data_in_leaf": 100,
    "feature_fraction": 0.5,
    "bagging_fraction": 0.8,
    "bagging_freq": 1,
    "lambda_l2": 1.0,
    "max_bin": 255,
    "metric": "average_precision",
    "num_threads": 4,
    "seed": 7,
    "deterministic": True,
    "force_row_wise": True,
    "verbose": -1,
}


def git_revision() -> str:
    try:
        return subprocess.run(["git", "rev-parse", "HEAD"], capture_output=True, text=True, check=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return "unknown"


def sigmoid(x: np.ndarray) -> np.ndarray:
    return 1 / (1 + np.exp(-x))


def train(features_dir: Path, out: Path, feature_set: str, budget: float, max_rounds: int,
          overrides: dict | None = None) -> dict:
    started = time.time()
    params = {**PARAMS, **(overrides or {})}
    data = prepare(features_dir, feature_set)
    tr, va, te = (data.splits[s] for s in ("train", "val", "test"))
    cat_idx = [data.features.index(c) for c in data.categorical]

    dtrain = lgb.Dataset(tr.x, tr.y, feature_name=data.features, categorical_feature=cat_idx, free_raw_data=True)
    dval = lgb.Dataset(va.x, va.y, reference=dtrain)
    model = lgb.train(params, dtrain, num_boost_round=max_rounds, valid_sets=[dval],
                      callbacks=[lgb.early_stopping(100, verbose=False), lgb.log_evaluation(100)])

    val_scores = model.predict(va.x, num_iteration=model.best_iteration)
    threshold = threshold_for_budget(val_scores, budget)
    test_raw = model.predict(te.x, num_iteration=model.best_iteration, raw_score=True)
    test_scores = sigmoid(test_raw)

    out.mkdir(parents=True, exist_ok=True)
    model.save_model(out / "model.txt", num_iteration=model.best_iteration)
    pl.DataFrame({"event_id": te.event_id, "raw_score": test_raw}).write_parquet(out / "test_predictions.parquet")
    pl.DataFrame({"event_id": va.event_id, "score": val_scores}).write_parquet(out / "val_predictions.parquet")

    manifest = json.loads((features_dir / "manifest.json").read_text())
    report = {
        "profile": manifest["profile"],
        "feature_set": feature_set,
        "model": "lightgbm",
        "schema_id": manifest["schema_id"],
        "features": data.features,
        "categorical": data.categorical,
        "vocab": data.vocab,
        "params": params,
        "best_iteration": model.best_iteration,
        "alert_budget": budget,
        "threshold": threshold,
        "label_delay": manifest["label_delay"],
        "split": {s: {"events": int(len(data.splits[s].y)), "fraud": int(data.splits[s].y.sum())}
                  for s in data.splits},
        "validation": evaluate(va.y, val_scores, threshold, va.amount),
        "test": evaluate(te.y, test_scores, threshold, te.amount),
        "test_budgets": at_budgets(val_scores, te.y, test_scores, te.amount),
        "features_sha256": data.features_sha256,
        "featurize_revision": manifest["git_revision"],
        "train_revision": git_revision(),
        "lightgbm_version": lgb.__version__,
        "train_seconds": round(time.time() - started, 1),
    }
    (out / "manifest.json").write_text(json.dumps(report, indent=2) + "\n")
    return report
