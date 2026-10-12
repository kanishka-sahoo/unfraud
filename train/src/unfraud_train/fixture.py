"""Small synthetic models that pin the Go evaluator to LightGBM's behaviour."""

from __future__ import annotations

from pathlib import Path

import lightgbm as lgb
import numpy as np


def write(out: Path) -> None:
    rng = np.random.default_rng(0)
    n = 4000
    x = rng.normal(size=(n, 6))
    x[:, 3] = rng.integers(0, 40, n)  # categorical with many values
    x[:, 4] = rng.integers(0, 3, n) * rng.normal(size=n)  # many exact zeros
    x[rng.random((n, 6)) < 0.15] = np.nan
    logit = np.nan_to_num(x[:, 0]) + np.isin(x[:, 3], [2, 5, 17, 33]) * 1.5 - np.nan_to_num(x[:, 4]) \
        + np.isnan(x[:, 5]) * 0.8 + rng.normal(size=n)
    y = (logit > 1).astype(int)
    out.mkdir(parents=True, exist_ok=True)

    # Inputs probe the edges: NaN, zero, negative and unseen categories.
    probe = x[:300].copy()
    probe[0, 3], probe[1, 3], probe[2, 3] = -1, 999, np.nan
    probe[3, 4], probe[4, 4] = 0.0, -0.0
    np.savetxt(out / "inputs.csv", probe, delimiter=",", fmt="%.17g")

    base = {"objective": "binary", "num_leaves": 15, "min_data_in_leaf": 20, "verbose": -1, "seed": 1,
            "deterministic": True, "num_threads": 1}
    for name, extra in {"default": {}, "zero_as_missing": {"zero_as_missing": True}}.items():
        model = lgb.train({**base, **extra}, lgb.Dataset(x, y, categorical_feature=[3]), 60)
        model.save_model(out / f"{name}.txt")
        np.savetxt(out / f"{name}.expected.csv", model.predict(probe, raw_score=True), fmt="%.17g")
