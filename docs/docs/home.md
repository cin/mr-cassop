---
title: Introduction
hide_title: true
slug: /
---

# mr-cassop

A Kubernetes operator for deploying and managing Apache Cassandra clusters across multiple regions.

## Overview

mr-cassop automates Cassandra cluster lifecycle management in Kubernetes. Its distinguishing feature is multi-region support: operators in separate Kubernetes clusters coordinate datacenter discovery, seed configuration, and region readiness so a single Cassandra deployment can span regions.

## Key Capabilities

🚀 **Automated Deployment** - Deploy Cassandra clusters with declarative YAML configuration  
📈 **Dynamic Scaling** - Scale clusters seamlessly with zero-downtime node operations  
🔄 **Backup & Restore** - Backup and restore workflows backed by Icarus  
🔧 **Repair Management** - Automated repair scheduling and optimization via Cassandra Reaper  
📊 **Full Observability** - Prometheus metrics, Grafana dashboards, and health monitoring  
🌍 **Multi-Region Ready** - Cross-region deployment with automatic datacenter awareness  
🔐 **Enterprise Security** - TLS encryption, RBAC, and network policy integration  
⚙️ **Operational Tooling** - Coordinated bootstrapping, rolling operations, and maintenance mode

## Architecture

mr-cassop consists of several integrated components:

- **Operator Controller**: Main reconciliation engine managing cluster state
- **Prober**: Health monitoring and metrics collection service  
- **Jolokia**: JMX metrics proxy for observability
- **Icarus**: Backup and restore service
- **Reaper**: Automated repair management service

## Getting Started

Ready to deploy your first Cassandra cluster? Start with our [Quickstart Guide](quickstart.md) for a step-by-step walkthrough.

For development and customization, check out the [Development Guide](development.md).

## Documentation

- [Quickstart](quickstart.md) - Get up and running quickly
- [CassandraCluster Configuration](cassandracluster-configuration.md) - Complete configuration reference
- [Backup & Restore](backup-restore.md) - Data protection strategies
- [Multi-Region Clusters](multi-region-cluster-configuration.md) - Cross-region deployment
- [Security](security/network-policies.md) - Authentication, encryption, and network policy topics
- [Development](development.md) - Contributing and customization

## Requirements

- **Kubernetes**: 1.28+ for deployments; envtest coverage uses Kubernetes 1.32 assets
- **Helm**: 3.15+ for installation
- **Resources**: Minimum 2 CPU cores and 4GB RAM per Cassandra node
- **Storage**: Persistent volumes for data persistence (recommended)

## License

mr-cassop is licensed under the Apache License 2.0.