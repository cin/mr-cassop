# mr-cassop

mr-cassop is a multi-region Kubernetes operator for deploying and managing Apache Cassandra clusters.

## Overview

mr-cassop automates Cassandra cluster lifecycle management in Kubernetes. Its distinguishing feature is support for Cassandra deployments that span multiple Kubernetes clusters/regions. It also includes controller-driven deployment, backup and restore integration through Icarus, repair scheduling through Reaper, Jolokia/Prober-based health checks, and Helm packaging.

## Status

This repository has been modernized from the original IBM Cassandra operator codebase and is under active maintenance. The current branch builds with Go 1.26, Kubernetes 1.36 libraries, and controller-runtime v0.24.1.

Unit, prober, and envtest integration tests are expected to pass locally. End-to-end tests still require a real Kubernetes cluster, registry access, and runtime validation of the selected Cassandra/Reaper/Icarus matrix before declaring the project production-ready.

## Key Features

- Automated deployment through `CassandraCluster` custom resources
- Backup and restore resources backed by Icarus
- Repair scheduling through Cassandra Reaper
- Multi-region topology support across Kubernetes clusters
- Prober-driven readiness and cross-region coordination
- Jolokia, Prometheus, and Grafana monitoring integration
- Optional TLS, authentication, and network policies

## Quick Start

Start with the project documentation:

- Live docs: [https://cin.github.io/mr-cassop/](https://cin.github.io/mr-cassop/)
- New users: [Quickstart Guide](docs/docs/quickstart.md)
- Developers: [Development Guide](docs/docs/development.md)
- Building images: [Docker Build Guide](DOCKER_BUILD.md)

### Local Demo

```bash
./build-local.sh --cassandra && \
kubectl create namespace mr-cassop-system && \
helm install mr-cassop ./mr-cassop -n mr-cassop-system -f local-values.yaml
```

See the [Quickstart Guide](docs/docs/quickstart.md) for detailed setup steps.

## Architecture

mr-cassop consists of integrated components working together:

- **Operator Controller** - Manages cluster lifecycle and reconciliation
- **Cassandra Nodes** - Core database instances in StatefulSets
- **Prober** - Health monitoring, readiness coordination, and multi-region discovery
- **Jolokia** - JMX-to-HTTP bridge used by prober and management flows
- **Reaper** - Automated repair management
- **Icarus** - Backup and restore service

See the [Architecture Overview](docs/docs/architecture-overview.md) for detailed component interactions and system diagrams.

## Documentation

- [Quickstart](docs/docs/quickstart.md)
- [Development](docs/docs/development.md)
- [CassandraCluster configuration](docs/docs/cassandracluster-configuration.md)
- [Backup and restore](docs/docs/backup-restore.md)
- [Multi-region cluster configuration](docs/docs/multi-region-cluster-configuration.md)
- [Security](docs/docs/security/)
- [Docker builds](DOCKER_BUILD.md)

### Run Documentation Locally

```bash
cd docs && npm install && npm start
```

## Requirements

- Kubernetes 1.28+ for deployments; envtest coverage uses Kubernetes 1.32.x assets.
- Cassandra 4.1.11 is the default supported image target.
- Go 1.26+ for local development.
- Node.js 24+ for documentation builds.
- Helm 3.15.4+ for installation and chart validation.
- Persistent volumes are recommended for Cassandra data.
- Minimum Cassandra node sizing depends on workload, but start with at least 2 CPU cores and 4 GiB RAM per node for non-trivial testing.

## Contributing

We welcome contributions! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and contribution guidelines.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
