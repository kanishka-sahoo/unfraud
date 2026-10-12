"""Evaluation at a fixed Alert Budget."""

from __future__ import annotations

import numpy as np
from sklearn.metrics import average_precision_score, roc_auc_score


def threshold_for_budget(scores: np.ndarray, budget: float) -> float:
    """The score above which `budget` of Events are flagged."""
    return float(np.quantile(scores, 1 - budget))


def evaluate(y: np.ndarray, scores: np.ndarray, threshold: float, amount: np.ndarray | None) -> dict:
    flagged = scores > threshold
    fraud = y == 1
    out = {
        "events": int(len(y)),
        "fraud": int(fraud.sum()),
        "pr_auc": float(average_precision_score(y, scores)),
        "roc_auc": float(roc_auc_score(y, scores)),
        "alert_rate": float(flagged.mean()),
        "recall": float((flagged & fraud).sum() / fraud.sum()),
        "precision": float((flagged & fraud).sum() / max(flagged.sum(), 1)),
    }
    if amount is not None:
        out["dollar_recall"] = float(amount[flagged & fraud].sum() / amount[fraud].sum())
    return out


BUDGETS = (0.005, 0.01, 0.02, 0.05)


def at_budgets(val_scores: np.ndarray, y: np.ndarray, scores: np.ndarray, amount: np.ndarray | None) -> dict:
    """Recall, $-recall and precision at each Alert Budget, with thresholds
    set on the validation month and applied unchanged to `scores`."""
    out = {}
    fraud = y == 1
    for b in BUDGETS:
        t = threshold_for_budget(val_scores, b)
        flagged = scores > t
        r = {
            "threshold": t,
            "alert_rate": float(flagged.mean()),
            "recall": float((flagged & fraud).sum() / fraud.sum()),
            "precision": float((flagged & fraud).sum() / max(flagged.sum(), 1)),
            # The best recall any model could reach flagging this many Events.
            "recall_ceiling": float(min(1.0, flagged.sum() / max(fraud.sum(), 1))),
        }
        if amount is not None:
            r["dollar_recall"] = float(amount[flagged & fraud].sum() / amount[fraud].sum())
        out[f"{b:g}"] = r
    return out


def f1_at(y: np.ndarray, scores: np.ndarray, t: float) -> dict:
    flagged = scores > t
    tp = int((flagged & (y == 1)).sum())
    p = tp / max(int(flagged.sum()), 1)
    r = tp / max(int((y == 1).sum()), 1)
    return {"threshold": float(t), "precision": p, "recall": r, "f1": 2 * p * r / (p + r) if p + r else 0.0}


def f1_report(val_y: np.ndarray, val_scores: np.ndarray, y: np.ndarray, scores: np.ndarray,
              groups: np.ndarray | None = None) -> dict:
    """Illicit-class F1 on test: at 0.5, and at the threshold that maximises
    F1 on validation; optionally per group (Elliptic timestep)."""
    candidates = np.unique(np.quantile(val_scores, np.linspace(0.5, 0.999, 400)))
    best = max(candidates, key=lambda t: f1_at(val_y, val_scores, t)["f1"])
    out = {"at_0.5": f1_at(y, scores, 0.5), "at_val_best": f1_at(y, scores, best)}
    if groups is not None:
        out["per_group_at_val_best"] = {
            int(g): f1_at(y[groups == g], scores[groups == g], best) | {"illicit": int(y[groups == g].sum())}
            for g in np.unique(groups)
        }
    return out
