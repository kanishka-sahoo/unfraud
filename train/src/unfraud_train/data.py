"""Loading Accuracy Run tables and turning them into model matrices."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import polars as pl

# Categorical vocabularies keep at most this many values per column; rarer and
# unseen values become missing, which LightGBM routes like any other NaN.
MAX_VOCAB = 1000

AMOUNT = "amount_usd"
FEATURE_SETS = ("static", "realtime", "graph")
SPLITS = ("train", "val", "test")


@dataclass
class Split:
    name: str
    event_id: np.ndarray
    x: np.ndarray  # float32, rows x features
    y: np.ndarray  # int8
    amount: np.ndarray


@dataclass
class Prepared:
    features: list[str]
    categorical: list[str]
    vocab: dict[str, list[str]]
    splits: dict[str, Split]
    features_sha256: str


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def feature_columns(m: dict, feature_set: str) -> tuple[list[str], list[str]]:
    """Numeric and categorical columns: raw features (static), plus the
    real-time features (realtime), plus the attribute-graph features (graph)."""
    raw_num, raw_cat, rt = m["raw_num"], m["raw_cat"], m["realtime"]
    sets = {
        "static": raw_num,
        "realtime": raw_num + [c for c in rt if not c.startswith("graph_")],
        "graph": raw_num + rt,
    }
    if feature_set not in sets:
        raise ValueError(f"feature sets are {FEATURE_SETS}, not {feature_set!r}")
    return sets[feature_set], raw_cat


def assign_splits(m: dict, times_ms: pl.Series) -> pl.Series:
    """The Profile's Event Time split; Events before training only warmed up
    the real-time features."""
    s = m["split"]
    return (
        pl.when(times_ms < s["train"]).then(pl.lit("warmup"))
        .when(times_ms < s["validation"]).then(pl.lit("train"))
        .when(times_ms < s["test"]).then(pl.lit("val"))
        .otherwise(pl.lit("test"))
    )


def build_vocab(values: pl.Series) -> list[str]:
    counts = values.drop_nulls().value_counts(sort=True)
    counts = counts.sort(["count", values.name], descending=[True, False])
    return counts[values.name].head(MAX_VOCAB).to_list()


def prepare(features_dir: Path, feature_set: str) -> Prepared:
    m = json.loads((features_dir / "manifest.json").read_text())
    num, cat = feature_columns(m, feature_set)

    path = features_dir / "features.parquet"
    all_ids = pl.read_parquet(path, columns=["event_id", "event_time_ms"]).with_row_index("row")
    labels = pl.read_parquet(features_dir / "labels.parquet", columns=["event_id", "label"])
    ids = all_ids.join(labels, on="event_id", how="inner").sort("row")
    ids = ids.with_columns(split=assign_splits(m, ids["event_time_ms"]))

    def column(c: str) -> pl.Series:
        return pl.read_parquet(path, columns=[c])[c]

    rows = {s: ids.filter(pl.col("split") == s)["row"].to_numpy() for s in SPLITS}
    columns = num + cat
    mats = {s: np.full((len(r), len(columns)), np.nan, dtype=np.float32, order="F") for s, r in rows.items()}

    # Fill matrices a column at a time so the whole table is never in memory.
    for j, c in enumerate(num):
        col = column(c).cast(pl.Float32).to_numpy()
        for s, r in rows.items():
            mats[s][:, j] = col[r]
    vocab: dict[str, list[str]] = {}
    for k, c in enumerate(cat):
        col = column(c)
        vocab[c] = build_vocab(col.gather(rows["train"]))
        codes = col.replace_strict({v: i for i, v in enumerate(vocab[c])}, default=None,
                                   return_dtype=pl.Float32).fill_null(np.nan).to_numpy()
        for s, r in rows.items():
            mats[s][:, len(num) + k] = codes[r]
    amount = column(AMOUNT).to_numpy()

    splits = {}
    for s, r in rows.items():
        part = ids.filter(pl.col("split") == s)
        splits[s] = Split(name=s, event_id=part["event_id"].to_numpy(), x=mats[s],
                          y=part["label"].to_numpy().astype(np.int8), amount=amount[r])
    return Prepared(features=columns, categorical=cat, vocab=vocab, splits=splits, features_sha256=sha256(path))
