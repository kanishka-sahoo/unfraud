# Deploy to local Kubernetes (kind) only, using operators for stateful parts

The system is production-shaped, but it must be rerunnable at no cost, so it targets a local kind cluster through one Helm chart, with Tilt for development. Kafka runs through Strimzi in KRaft mode and Postgres through CloudNativePG; Valkey and SeaweedFS run as plain StatefulSets. SeaweedFS was chosen over MinIO because MinIO's community edition is in maintenance mode, and Bitnami charts are avoided because of their 2025 catalog changes. A `lite` values profile (1 broker, no standby) supports smaller machines.

## Considered Options

- docker-compose: easier, but it can't demonstrate KEDA autoscaling, operators, standby failover or Kubernetes-native observability.
- A cloud deployment with Terraform: costs money to rerun; rejected by scope.
