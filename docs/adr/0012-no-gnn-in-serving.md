# No GNN in the serving path; graph signal reaches the model as online graph features

Elliptic is served by LightGBM over local features plus online neighbor aggregates computed by graph-svc. Serving a GNN from Go would need ONNX export and neighbor sampling at request time, and the published evidence (Weber et al. 2019: random forest beats GCN on Elliptic) doesn't justify that cost. Revisit if a GNN baseline clearly beats the served model.

## Amendment (milestone 4)

No GraphSAGE baseline was trained. The comparison point is the published GCN result on the same test period (Weber et al. 2019, illicit F1 0.628); the served LightGBM with streamed flow-graph features reaches 0.780 (`results/elliptic-lightgbm.md`).
