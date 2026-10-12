# Models ship as OCI images; no object store

A trained model (`model.txt` plus `manifest.json`) is packaged as a tiny image, `unfraud/model-<profile>:<sha256 of model.txt, 12 chars>`. An init container copies it into the scorer pod. The image registry is therefore the model registry: the tag is the model version (and is stamped on every Decision as `model_version`), promotion is a tag change in Helm values, and a Shadow Model is a second image.

The design originally put artifacts in an S3-compatible store (SeaweedFS) with manifests in Postgres. On the 8 GB reference machine that is another always-on stateful service for files that change a few times a week. Images reuse machinery the cluster already has (kind image loading, Helm values, rollouts and rollbacks), and graph-svc snapshots go to a PersistentVolumeClaim instead, so SeaweedFS is dropped entirely.

## Consequences

- Models over a few hundred MB would make image pulls slow; ours are about 1 MB.
- Model lineage (training data hash, git revisions, metrics) lives in the manifest inside the image rather than in a database.
