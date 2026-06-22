---
title: Jolokia
slug: /jolokia
---

Jolokia provides HTTP access to JMX. mr-cassop uses it as the JMX transport for management and health workflows that need to inspect Cassandra state from inside Kubernetes.

## Deployment Model

For each `CassandraCluster`, the operator deploys Jolokia in the prober Deployment. The prober container uses Jolokia to query Cassandra JMX state such as node status and cluster membership. Cassandra pods themselves run the Cassandra container and the Icarus sidecar; Jolokia is not injected into every Cassandra pod.

## Image Configuration

The chart-level `jolokiaImage` value controls the default image used by managed clusters. Leave it empty to use:

```text
ghcr.io/cin/mr-cassop/jolokia:<chart appVersion>
```

You can override the image for a specific `CassandraCluster` with:

```yaml
spec:
  prober:
    jolokia:
      image: ghcr.io/cin/mr-cassop/jolokia:custom
      imagePullPolicy: IfNotPresent
```

## TLS and JMX Authentication

When client TLS or JMX authentication settings are enabled, the operator mounts the relevant certificates and credentials into the prober/Jolokia pod so prober can continue to query Cassandra safely.

For most users, Jolokia does not need direct interaction. Configure it only when you need to override the image, resources, or pull policy used by the prober deployment.
