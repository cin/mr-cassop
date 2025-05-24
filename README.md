# MIGRATION IN PROGRESS! Not ready for use...yet

# mr-cassop

A production-ready Kubernetes operator for deploying and managing Apache Cassandra clusters across multiple regions.

## Overview

mr-cassop automates the deployment, configuration, and management of Cassandra instances in Kubernetes clusters. It provides comprehensive cluster lifecycle management, backup/restore capabilities, monitoring integration, and multi-region support.

## Status

✅ **Active Development** - The operator is functional and ready for use  
✅ **Production Ready** - Successfully managing Cassandra clusters in production environments  
✅ **Modern Codebase** - Updated to Go 1.24 and controller-runtime v0.20.4  
✅ **Comprehensive Testing** - Unit, integration, and e2e test coverage  
✅ **Docker Build System** - Optimized multi-platform builds for development and production

## Key Features

- 🚀 **Automated Deployment** - Declarative cluster configuration via Custom Resources
- 📈 **Dynamic Scaling** - Seamless cluster scaling with automatic token rebalancing  
- 🔄 **Backup & Restore** - Integrated backup capabilities via Icarus with point-in-time recovery
- 🔧 **Repair Management** - Automated repair scheduling via Cassandra Reaper
- 📊 **Monitoring** - Built-in metrics collection with Jolokia and Prometheus integration
- 🌍 **Multi-Region** - Cross-region cluster deployment with automatic datacenter awareness
- 🔐 **Security** - Role-based authentication, TLS encryption, and network policies
- ⚡ **High Performance** - Optimized for production workloads

## Quick Start

Ready to get started? Follow our comprehensive guides:

- 📚 **New Users**: Start with the [Quickstart Guide](docs/docs/quickstart.md)
- 👨‍💻 **Developers**: See the [Development Guide](docs/docs/development.md)  
- 🏗️ **Building**: Check the [Docker Build Guide](DOCKER_BUILD.md)

### One-Command Demo

```bash
# For the impatient - full local setup:
./build-local.sh --cassandra && \
kubectl create namespace mr-cassop-system cassop && \
helm install mr-cassop ./mr-cassop -n mr-cassop-system -f local-values.yaml
```

See the [Quickstart Guide](docs/docs/quickstart.md) for detailed steps and explanations.

## Architecture

mr-cassop consists of integrated components working together:

- **Operator Controller** - Manages cluster lifecycle and reconciliation
- **Cassandra Nodes** - Core database instances in StatefulSets
- **Prober** - Health monitoring and metrics collection
- **Jolokia** - JMX-to-HTTP bridge for observability  
- **Reaper** - Automated repair management
- **Icarus** - Backup and restore service

See the [Architecture Overview](docs/docs/architecture-overview.md) for detailed component interactions and system diagrams.

## Documentation

| Topic | Description |
|-------|-------------|
| [🚀 Quickstart](docs/docs/quickstart.md) | Get up and running in minutes |
| [🏗️ Development](docs/docs/development.md) | Contributing and development setup |
| [⚙️ Configuration](docs/docs/cassandracluster-configuration.md) | Complete configuration reference |
| [💾 Backup & Restore](docs/docs/backup-restore.md) | Data protection strategies |
| [🌍 Multi-Region](docs/docs/multi-region-cluster-configuration.md) | Cross-region deployment |
| [🔐 Security](docs/docs/security/) | Authentication and encryption |
| [🏗️ Docker Builds](DOCKER_BUILD.md) | Build system documentation |

### Run Documentation Locally

```bash
cd docs && npm install && npm start
```

## Requirements

- **Kubernetes**: 1.19+ (tested with 1.24+)
- **Helm**: 3.7+ for installation  
- **Resources**: Minimum 2 CPU cores and 4GB RAM per Cassandra node
- **Storage**: Persistent volumes recommended for production

## Contributing

We welcome contributions! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and contribution guidelines.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
