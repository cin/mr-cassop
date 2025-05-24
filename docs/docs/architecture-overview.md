---
title: Architecture Overview
slug: /architecture-overview
---

# mr-cassop Architecture

mr-cassop is a comprehensive Kubernetes operator that deploys and manages Cassandra clusters along with essential supporting components for monitoring, backup, and maintenance.

## Core Components

A `CassandraCluster` managed by mr-cassop consists of the following integrated components:

### 🎯 **Operator Controller**
- **Purpose**: Main reconciliation engine that manages cluster state
- **Responsibilities**: 
  - Watches CassandraCluster resources
  - Creates and manages StatefulSets, Services, and ConfigMaps
  - Handles scaling operations and rolling updates
  - Coordinates with other components
- **Deployment**: Runs as a Deployment in the `mr-cassop-system` namespace

### 🗄️ **Cassandra Nodes**
- **Purpose**: The core Apache Cassandra database instances
- **Deployment**: Managed by StatefulSets (one per datacenter)
- **Features**:
  - Persistent storage for data
  - Automatic cluster formation
  - JMX monitoring enabled
  - Authentication and authorization configured

### 🔍 **Prober**
- **Purpose**: Health monitoring and cross-region communication
- **Responsibilities**:
  - Readiness and liveness checks
  - Metrics collection and exposition
  - Inter-datacenter health monitoring
  - Node status aggregation
- **Deployment**: Sidecar container in each Cassandra pod
- **Documentation**: [Prober Details](/prober.md)

### 📊 **Jolokia**
- **Purpose**: JMX-to-HTTP bridge for metrics collection
- **Responsibilities**:
  - Exposes Cassandra JMX metrics over HTTP
  - Provides secure access to management operations
  - Enables monitoring integrations
- **Deployment**: Sidecar container in each Cassandra pod
- **Website**: [jolokia.org](https://jolokia.org/)

### 🔧 **Reaper**
- **Purpose**: Automated repair management and scheduling
- **Responsibilities**:
  - Cluster repair coordination
  - Repair schedule management
  - Repair progress monitoring
  - Performance optimization
- **Deployment**: Separate Deployment per cluster
- **Website**: [cassandra-reaper.io](http://cassandra-reaper.io/)
- **Documentation**: [Reaper Configuration](/reaper.md)

### 💾 **Icarus**
- **Purpose**: Backup and restore service
- **Responsibilities**:
  - Automated backup creation
  - Point-in-time recovery
  - Cross-region backup replication
  - Backup lifecycle management
- **Deployment**: Sidecar container in each Cassandra pod

## System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kubernetes Cluster                      │
├─────────────────────────────────────────────────────────────────┤
│  mr-cassop-system namespace                                     │
│  ┌─────────────────┐                                           │
│  │   mr-cassop     │                                           │
│  │   (Operator)    │ ◄────── Watches CassandraCluster CRDs    │
│  └─────────────────┘                                           │
├─────────────────────────────────────────────────────────────────┤
│  Application namespace (e.g., cassop)                          │
│                                                                 │
│  ┌─────────────────┐    ┌─────────────────┐                   │
│  │     Reaper      │    │   Monitoring    │                   │
│  │   (Repairs)     │    │   Dashboard     │                   │
│  └─────────────────┘    └─────────────────┘                   │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              Cassandra StatefulSet (dc1)               │   │
│  │                                                         │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐                 │   │
│  │  │  Pod-0  │  │  Pod-1  │  │  Pod-2  │                 │   │
│  │  │         │  │         │  │         │                 │   │
│  │  │ ┌─────┐ │  │ ┌─────┐ │  │ ┌─────┐ │                 │   │
│  │  │ │ C*  │ │  │ │ C*  │ │  │ │ C*  │ │                 │   │
│  │  │ └─────┘ │  │ └─────┘ │  │ └─────┘ │                 │   │
│  │  │ ┌─────┐ │  │ ┌─────┐ │  │ ┌─────┐ │                 │   │
│  │  │ │Prober│ │  │ │Prober│ │  │ │Prober│ │                 │   │
│  │  │ └─────┘ │  │ └─────┘ │  │ └─────┘ │                 │   │
│  │  │ ┌─────┐ │  │ ┌─────┐ │  │ ┌─────┐ │                 │   │
│  │  │ │Jolokia│ │  │ │Jolokia│ │  │ │Jolokia│ │                 │   │
│  │  │ └─────┘ │  │ └─────┘ │  │ └─────┘ │                 │   │
│  │  │ ┌─────┐ │  │ ┌─────┐ │  │ ┌─────┐ │                 │   │
│  │  │ │Icarus│ │  │ │Icarus│ │  │ │Icarus│ │                 │   │
│  │  │ └─────┘ │  │ └─────┘ │  │ └─────┘ │                 │   │
│  │  └─────────┘  └─────────┘  └─────────┘                 │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Component Interactions

1. **Operator Controller** continuously watches for CassandraCluster resources
2. **StatefulSets** are created/updated based on cluster specifications
3. **Prober** containers monitor Cassandra health and report status
4. **Jolokia** exposes JMX metrics for monitoring and management
5. **Reaper** automatically schedules and executes repairs
6. **Icarus** handles backup operations based on configured schedules
7. **Services** provide stable networking for client connections

## Multi-Datacenter Support

For multi-region deployments, mr-cassop creates:
- One StatefulSet per datacenter
- Cross-datacenter networking configuration  
- Automatic seed node management
- Region-aware backup strategies

See [Multi-Region Configuration](multi-region-cluster-configuration.md) for details.

## Monitoring Integration

The architecture supports comprehensive monitoring through:
- Prometheus metrics from Jolokia and Prober
- Grafana dashboards for visualization
- Kubernetes native health checks
- Custom alerts and notifications

See [Monitoring Documentation](monitoring.md) for configuration details.