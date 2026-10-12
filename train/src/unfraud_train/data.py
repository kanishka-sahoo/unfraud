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


@dataclass
class Split:
    name: str
    event_id: np.ndarray
    x: np.ndarray  # float32, rows x features
    y: np.ndarray  # int8
    amount: np.ndarray | None


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


def month_splits(times_ms: pl.Series, train: int, val: int) -> pl.Series:
    """Assigns calendar months in Event Time order: the first `train` months
    train, the next `val` validate, the rest test."""
    months = pl.from_epoch(times_ms, time_unit="ms").dt.strftime("%Y-%m")
    order = {m: i for i, m in enumerate(sorted(months.unique().to_list()))}
    idx = months.replace_strict(order, return_dtype=pl.Int32)
    return (
        pl.when(idx < train).then(pl.lit("train"))
        .when(idx < train + val).then(pl.lit("val"))
        .otherwise(pl.lit("test"))
    )


def build_vocab(values: pl.Series) -> list[str]:
    counts = values.drop_nulls().value_counts(sort=True)
    counts = counts.sort(["count", values.name], descending=[True, False])
    return counts[values.name].head(MAX_VOCAB).to_list()


def prepare(features_dir: Path, feature_set: str, train_months: int = 4, val_months: int = 1,
            amount_column: str | None = "TransactionAmt") -> Prepared:
    manifest = json.loads((features_dir / "manifest.json").read_text())
    num = list(manifest["raw_num"])
    cat = list(manifest["raw_cat"])
    if feature_set == "realtime":
        num += [c for c in manifest["realtime"] if not c.startswith("graph_")]
    elif feature_set == "graph":
        num += manifest["realtime"]
    elif feature_set != "static":
        raise ValueError(f"unknown feature set {feature_set!r}")

    path = features_dir / "features.parquet"
    ids = pl.read_parquet(path, columns=["event_id", "event_time_ms"])
    labels = pl.read_parquet(features_dir / "labels.parquet", columns=["event_id", "label"])
    ids = ids.with_row_index("row").join(labels, on="event_id", how="inner").sort("row")
    split = month_splits(ids["event_time_ms"], train_months, val_months)
    ids = ids.with_columns(split.alias("split"))

    rows = {s: ids.filter(pl.col("split") == s)["row"].to_numpy() for s in ("train", "val", "test")}
    columns = num + cat
    mats = {s: np.full((len(r), len(columns)), np.nan, dtype=np.float32, order="F") for s, r in rows.items()}

    # Fill matrices a column at a time so the whole table is never in memory.
    for j, c in enumerate(num):
        col = pl.read_parquet(path, columns=[c])[c].cast(pl.Float32).to_numpy()
        for s, r in rows.items():
            mats[s][:, j] = col[r]
    vocab: dict[str, list[str]] = {}
    for k, c in enumerate(cat):
        col = pl.read_parquet(path, columns=[c])[c]
        vocab[c] = build_vocab(col.gather(rows["train"]))
        codes = col.replace_strict({v: i for i, v in enumerate(vocab[c])}, default=None,
                                   return_dtype=pl.Float32).fill_null(np.nan).to_numpy()
        for s, r in rows.items():
            mats[s][:, len(num) + k] = codes[r]

    amount = None
    if amount_column:
        amount = pl.read_parquet(path, columns=[amount_column])[amount_column].to_numpy()

    splits = {}
    for s, r in rows.items():
        part = ids.filter(pl.col("split") == s)
        splits[s] = Split(
            name=s,
            event_id=part["event_id"].to_numpy(),
            x=mats[s],
            y=part["label"].to_numpy().astype(np.int8),
            amount=None if amount is None else amount[r],
        )
    return Prepared(features=columns, categorical=cat, vocab=vocab, splits=splits, features_sha256=sha256(path))
