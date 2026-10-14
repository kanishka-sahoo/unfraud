"""Calibrating a model's scores into probabilities.

A boosted model's raw scores rank Events well but are not probabilities: with
deep trees and thousands of rounds it is overconfident, and the fraud rate of
the training years is not that of later ones. Platt scaling fits
p = sigmoid(slope * raw + intercept) on the validation period. It is linear in
log-odds, so it keeps the ranking (and every Alert Budget's Events) and keeps
TreeSHAP Contributions additive: each one is scaled by the slope, and the bias
becomes slope * bias + intercept.
"""

from __future__ import annotations

import numpy as np
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import brier_score_loss, log_loss

# Reliability bins over predicted probability; fraud is rare, so most Events
# fall in the first bins and the edges are roughly logarithmic.
EDGES = (0.0, 1e-4, 1e-3, 1e-2, 0.05, 0.1, 0.2, 0.5, 1.0)


def sigmoid(x: np.ndarray) -> np.ndarray:
    return 1 / (1 + np.exp(-x))


def fit_platt(raw: np.ndarray, y: np.ndarray) -> dict:
    lr = LogisticRegression(penalty=None, tol=1e-10, max_iter=1000).fit(raw.reshape(-1, 1), y)
    return {"method": "platt", "fit_on": "validation",
            "slope": float(lr.coef_[0][0]), "intercept": float(lr.intercept_[0])}


def apply(raw: np.ndarray, cal: dict) -> np.ndarray:
    return sigmoid(cal["slope"] * raw + cal["intercept"])


def reliability(y: np.ndarray, p: np.ndarray) -> dict:
    """How well probabilities match outcomes: Brier score, log loss, expected
    against actual fraud, and per bin the mean probability against the
    observed fraud rate. The expected calibration error weights each bin's
    gap by its share of Events."""
    bins, ece = [], 0.0
    idx = np.digitize(p, EDGES[1:-1])
    for k in range(len(EDGES) - 1):
        m = idx == k
        if not m.any():
            continue
        mean_p, rate = float(p[m].mean()), float(y[m].mean())
        ece += m.mean() * abs(mean_p - rate)
        bins.append({"from": EDGES[k], "to": EDGES[k + 1], "events": int(m.sum()),
                     "mean_probability": mean_p, "fraud_rate": rate, "fraud": int(y[m].sum())})
    return {"brier": float(brier_score_loss(y, p)), "log_loss": float(log_loss(y, p, labels=[0, 1])),
            "expected_fraud": float(p.sum()), "fraud": int(y.sum()), "ece": float(ece), "bins": bins}
