# The runtime is pure Go: our own LightGBM evaluator and TreeSHAP

Python exists only in the offline `train` CLI. Serving loads LightGBM's text model format with `internal/model/lgbm`, a direct port of LightGBM's decision rules (numerical thresholds, categorical bitsets, and the NaN and zero missing-value modes) in about 300 lines with no cgo. Contributions will come from a Go TreeSHAP over the same tree structures. Parity is enforced twice:

- in CI, against committed fixtures where LightGBM's own raw scores cover every routing edge (negative, unseen and missing categories; ±0 under `zero_as_missing`), to within 1e-12;
- on real data (`make parity`), where the Go path from the raw features table through `model.Encode` must match Python's test-month scores to within 1e-6.

Model inputs are float32 in training and rounded to float32 in Go, so split thresholds compare identical values. CatBoost was planned as an offline comparison (moving to serving through ONNX Runtime only if it clearly won) but has not been trained.

## Considered Options

- `leaves` (the original plan): it rejects LightGBM 4.x models ("only version=v3 is supported"), is unmaintained, and doesn't expose node counts, which TreeSHAP needs.
- A Python inference or SHAP sidecar: an extra network hop and language in the runtime.
- ONNX Runtime for everything: cgo in every service, for no gain with LightGBM.
