# No GNN in the serving path; graph signal reaches the model as online graph features

Elliptic is served by LightGBM over local features plus online neighbor aggregates computed by graph-svc. A GraphSAGE model is trained by the `train` CLI only as a published baseline. Serving a GNN from Go would need ONNX export and neighbor sampling at request time, and the published evidence (Weber et al. 2019: random forest beats GCN on Elliptic) doesn't justify that cost. Revisit if the GraphSAGE baseline clearly beats the served model.
