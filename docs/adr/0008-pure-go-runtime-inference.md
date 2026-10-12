# The runtime is pure Go: LightGBM through leaves, TreeSHAP implemented in Go

Python exists only in the offline `train` CLI. LightGBM is served in-process through `leaves` (no cgo), and Contributions come from a Go TreeSHAP implementation. CI checks both against Python LightGBM (`predict` and `pred_contrib`) to within 1e-6. CatBoost, which may score better on IEEE-CIS's high-cardinality categoricals, is trained and reported; it moves to serving through ONNX Runtime (cgo) only if it clearly wins.

## Considered Options

- A Python inference or SHAP sidecar: an extra network hop and language in the runtime.
- ONNX Runtime for everything: cgo in every service, for no gain with LightGBM.
