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

# The Payment Schema's core features (internal/profile/payments): the same
# names and meaning in every payment Profile.
CORE_NUM = ["amount_usd", "hour", "weekday"]
CORE_CAT = ["channel", "category"]
AMOUNT = "amount_usd"

FEATURE_SETS = ("core", "static", "realtime", "graph")


@dataclass
class Split:
    name: str
    event_id: np.ndarray
    x: np.ndarray  # float32, rows x features
    y: np.ndarray  # int8
    amount: np.ndarray
    source: np.ndarray  # index into Prepared.sources, per row


@dataclass
class Prepared:
    features: list[str]
    categorical: list[str]
    vocab: dict[str, list[str]]
    splits: dict[str, Split]
    sources: list[str]  # profile names
    features_sha256: dict[str, str]


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def manifest(features_dir: Path) -> dict:
    return json.loads((features_dir / "manifest.json").read_text())


def has_graph(m: dict) -> bool:
    return any(c.startswith("graph_") for c in m["realtime"])


def feature_columns(m: dict, feature_set: str) -> tuple[list[str], list[str]]:
    """Numeric and categorical columns of a feature set:

    - core: the core raw features plus the real-time features, which every
      payment Profile shares, so the model transfers between Profiles
    - static: every raw feature, core and the Profile's extras
    - realtime: static plus the real-time features
    - graph: realtime plus the attribute-graph features
    """
    raw_num, raw_cat, rt = m["raw_num"], m["raw_cat"], m["realtime"]
    local = [c for c in rt if not c.startswith("graph_")]
    if feature_set == "core":
        return CORE_NUM + local, CORE_CAT
    if feature_set == "static":
        return raw_num, raw_cat
    if feature_set == "realtime":
        return raw_num + local, raw_cat
    if feature_set == "graph":
        if not has_graph(m):
            raise ValueError(f"{m['profile']} has no graph features")
        return raw_num + rt, raw_cat
    raise ValueError(f"feature sets are {FEATURE_SETS}, not {feature_set!r}")


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


SPLITS = ("train", "val", "test")


def prepare(features_dirs: list[Path], feature_set: str, vocab: dict[str, list[str]] | None = None) -> Prepared:
    """Builds matrices from one or more Profiles' tables (pooled). With
    several, the feature set must name the same columns in each, so only
    "core" pools. A given vocab (from a trained model) is reused as is."""
    manifests = [manifest(d) for d in features_dirs]
    cols = [feature_columns(m, feature_set) for m in manifests]
    if any(c != cols[0] for c in cols):
        raise ValueError(f"feature set {feature_set!r} differs between {[m['profile'] for m in manifests]}")
    num, cat = cols[0]

    parts = []  # per source: (path, all_ids, ids with split)
    for i, (d, m) in enumerate(zip(features_dirs, manifests)):
        path = d / "features.parquet"
        all_ids = pl.read_parquet(path, columns=["event_id", "event_time_ms"]).with_row_index("row")
        labels = pl.read_parquet(d / "labels.parquet", columns=["event_id", "label"])
        ids = all_ids.join(labels, on="event_id", how="inner").sort("row")
        ids = ids.with_columns(split=assign_splits(m, ids["event_time_ms"]), source=pl.lit(i, pl.Int8))
        parts.append((path, ids))

    rows = {s: [ids.filter(pl.col("split") == s)["row"].to_numpy() for _, ids in parts] for s in SPLITS}
    columns = num + cat
    mats = {s: np.full((sum(len(r) for r in rows[s]), len(columns)), np.nan, dtype=np.float32, order="F")
            for s in SPLITS}

    def fill(j: int, per_source: list[np.ndarray]) -> None:
        for s in SPLITS:
            mats[s][:, j] = np.concatenate([col[r] for col, r in zip(per_source, rows[s])])

    # Fill matrices a column at a time so a whole table is never in memory.
    for j, c in enumerate(num):
        fill(j, [pl.read_parquet(p, columns=[c])[c].cast(pl.Float32).to_numpy() for p, _ in parts])
    fixed = vocab is not None
    vocab = dict(vocab or {})
    for k, c in enumerate(cat):
        raw = [pl.read_parquet(p, columns=[c])[c] for p, _ in parts]
        if not fixed:
            vocab[c] = build_vocab(pl.concat([col.gather(r) for col, r in zip(raw, rows["train"])]))
        codes = {v: i for i, v in enumerate(vocab[c])}
        fill(len(num) + k, [col.replace_strict(codes, default=None, return_dtype=pl.Float32)
                            .fill_null(np.nan).to_numpy() for col in raw])
    amounts = [pl.read_parquet(p, columns=[AMOUNT])[AMOUNT].to_numpy() for p, _ in parts]

    splits = {}
    for s in SPLITS:
        sel = [ids.filter(pl.col("split") == s) for _, ids in parts]
        splits[s] = Split(
            name=s,
            event_id=np.concatenate([x["event_id"].to_numpy() for x in sel]),
            x=mats[s],
            y=np.concatenate([x["label"].to_numpy() for x in sel]).astype(np.int8),
            amount=np.concatenate([a[r] for a, r in zip(amounts, rows[s])]),
            source=np.concatenate([x["source"].to_numpy() for x in sel]),
        )
    return Prepared(features=columns, categorical=cat, vocab=vocab, splits=splits,
                    sources=[m["profile"] for m in manifests],
                    features_sha256={m["profile"]: sha256(p) for m, (p, _) in zip(manifests, parts)})
