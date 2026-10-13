"""Training a LightGBM model on one or more Accuracy Runs' tables."""

from __future__ import annotations

import json
import subprocess
import time
from pathlib import Path

import lightgbm as lgb
import numpy as np
import polars as pl

from .data import Prepared, Split, manifest, prepare
from .metrics import at_budgets, evaluate

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


def per_source(data: Prepared, val_scores: np.ndarray, test_scores: np.ndarray, budget: float) -> dict:
    """Validation-set thresholds and test metrics for each source on its
    own: a pooled model still serves each dataset's Events separately."""
    va, te = data.splits["val"], data.splits["test"]
    out = {}
    for i, name in enumerate(data.sources):
        v, t = va.source == i, te.source == i
        threshold = float(np.quantile(val_scores[v], 1 - budget))
        out[name] = {
            "split": {s.name: {"events": int((s.source == i).sum()), "fraud": int(s.y[s.source == i].sum())}
                      for s in data.splits.values()},
            "threshold": threshold,
            "validation": evaluate(va.y[v], val_scores[v], threshold, va.amount[v]),
            "test": evaluate(te.y[t], test_scores[t], threshold, te.amount[t]),
            "test_budgets": at_budgets(val_scores[v], te.y[t], test_scores[t], te.amount[t]),
        }
    return out


def train(features_dirs: list[Path], out: Path, feature_set: str, budget: float, max_rounds: int,
          overrides: dict | None = None) -> dict:
    started = time.time()
    params = {**PARAMS, **(overrides or {})}
    data = prepare(features_dirs, feature_set)
    tr, va, te = (data.splits[s] for s in ("train", "val", "test"))
    cat_idx = [data.features.index(c) for c in data.categorical]

    dtrain = lgb.Dataset(tr.x, tr.y, feature_name=data.features, categorical_feature=cat_idx, free_raw_data=True)
    dval = lgb.Dataset(va.x, va.y, reference=dtrain)
    model = lgb.train(params, dtrain, num_boost_round=max_rounds, valid_sets=[dval],
                      callbacks=[lgb.early_stopping(100, verbose=False), lgb.log_evaluation(100)])

    val_scores = model.predict(va.x, num_iteration=model.best_iteration)
    test_raw = model.predict(te.x, num_iteration=model.best_iteration, raw_score=True)
    test_scores = sigmoid(test_raw)

    out.mkdir(parents=True, exist_ok=True)
    model.save_model(out / "model.txt", num_iteration=model.best_iteration)
    sources = np.array(data.sources)
    pl.DataFrame({"event_id": te.event_id, "source": sources[te.source], "raw_score": test_raw}) \
        .write_parquet(out / "test_predictions.parquet")
    pl.DataFrame({"event_id": va.event_id, "source": sources[va.source], "score": val_scores}) \
        .write_parquet(out / "val_predictions.parquet")

    manifests = [manifest(d) for d in features_dirs]
    results = per_source(data, val_scores, test_scores, budget)
    first = results[data.sources[0]]
    report = {
        "profile": "+".join(data.sources),
        "sources": data.sources,
        "feature_set": feature_set,
        "model": "lightgbm",
        # Core models bind to any payment Profile by feature name (schema 0).
        "schema_id": 0 if feature_set == "core" else manifests[0]["schema_id"],
        "features": data.features,
        "categorical": data.categorical,
        "vocab": data.vocab,
        "params": params,
        "best_iteration": model.best_iteration,
        "alert_budget": budget,
        "label_delay": {m["profile"]: m["label_delay"] for m in manifests},
        # A single source's results at the top level, as the scorer reads them.
        "threshold": first["threshold"],
        "split": first["split"],
        "validation": first["validation"],
        "test": first["test"],
        "test_budgets": first["test_budgets"],
        "per_source": results,
        "features_sha256": data.features_sha256,
        "featurize_revision": {m["profile"]: m["git_revision"] for m in manifests},
        "train_revision": git_revision(),
        "lightgbm_version": lgb.__version__,
        "train_seconds": round(time.time() - started, 1),
    }
    (out / "manifest.json").write_text(json.dumps(report, indent=2) + "\n")
    return report


def transfer(model_dir: Path, features_dir: Path) -> dict:
    """Scores another Profile's validation and test periods with a trained
    core model, unchanged. Thresholds come from that Profile's validation
    period, as a team adopting the model would set them."""
    m = json.loads((model_dir / "manifest.json").read_text())
    if m["schema_id"] != 0:
        raise SystemExit(f"{model_dir} is not a core model; only core models transfer")
    data = prepare([features_dir], m["feature_set"], vocab=m["vocab"])
    if data.features != m["features"]:
        raise SystemExit("feature columns differ from the model's")
    booster = lgb.Booster(model_file=str(model_dir / "model.txt"))
    va, te = data.splits["val"], data.splits["test"]
    return per_source(data, booster.predict(va.x), booster.predict(te.x), m["alert_budget"])[data.sources[0]]


__all__ = ["train", "transfer", "Split"]
