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


def feature_columns(manifest: dict, feature_set: str) -> tuple[list[str], list[str]]:
    """Numeric and categorical columns of a feature set, per profile."""
    raw_num, raw_cat, rt = manifest["raw_num"], manifest["raw_cat"], manifest["realtime"]
    if manifest["profile"] == "elliptic":
        local = [c for c in raw_num if c.startswith("local_")]
        sets = {
            "local": local,
            "flow": local + manifest.get("flow", []),
            "dataset_agg": local + [c for c in raw_num if c.startswith("agg_")],
        }
        if feature_set not in sets:
            raise ValueError(f"elliptic feature sets are {list(sets)}, not {feature_set!r}")
        return sets[feature_set], []
    sets = {
        "static": raw_num,
        "realtime": raw_num + [c for c in rt if not c.startswith("graph_")],
        "graph": raw_num + rt,
    }
    if feature_set not in sets:
        raise ValueError(f"feature sets are {list(sets)}, not {feature_set!r}")
    return sets[feature_set], raw_cat


def assign_splits(manifest: dict, features_dir: Path, ids: pl.DataFrame) -> pl.Series:
    """Event Time splits: IEEE-CIS by calendar month (4 train, 1 validation,
    the rest test); Elliptic by timestep (1-29 train, 30-34 validation, 35-49
    test, so the test period matches Weber et al. 2019)."""
    if manifest["profile"] == "elliptic":
        ts = pl.read_parquet(features_dir / "features.parquet", columns=["timestep"])["timestep"].to_numpy()[ids["row"].to_numpy()]
        ts = pl.Series(ts)
        return (pl.when(ts <= 29).then(pl.lit("train")).when(ts <= 34).then(pl.lit("val")).otherwise(pl.lit("test")))
    return month_splits(ids["event_time_ms"], 4, 1)


def prepare(features_dir: Path, feature_set: str) -> Prepared:
    manifest = json.loads((features_dir / "manifest.json").read_text())
    num, cat = feature_columns(manifest, feature_set)
    flow = set(manifest.get("flow", []))

    path = features_dir / "features.parquet"
    all_ids = pl.read_parquet(path, columns=["event_id", "event_time_ms"]).with_row_index("row")
    labels = pl.read_parquet(features_dir / "labels.parquet", columns=["event_id", "label"])
    ids = all_ids.join(labels, on="event_id", how="inner").sort("row")
    ids = ids.with_columns(pl.Series("split", pl.select(assign_splits(manifest, features_dir, ids)).to_series()))

    def column(c: str) -> pl.Series:
        if c in flow:
            rev = pl.read_parquet(features_dir / "revised.parquet", columns=["event_id", c])
            return all_ids.join(rev, on="event_id", how="left").sort("row")[c]
        return pl.read_parquet(path, columns=[c])[c]

    rows = {s: ids.filter(pl.col("split") == s)["row"].to_numpy() for s in ("train", "val", "test")}
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

    amount = None
    if "TransactionAmt" in manifest["raw_num"]:
        amount = column("TransactionAmt").to_numpy()

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
