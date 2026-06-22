---
title: Maintenance Mode
slug: /maintenance-mode
---

The maintenance section in the `CassandraCluster` spec allows clients to temporarily disable replicas in a Cassandra cluster for debugging purposes, such as performing a backup of the SSTables. While the selected replicas are in maintenance mode, they will not communicate with other Cassandra nodes.
Users of the maintenance CR must be aware that putting too many pods into maintenance mode may have undesirable effects. For example, enabling dc maintenance mode for a single dc cluster would effectively take down the cluster.

A user may enable maintenance mode for any Cassandra pod in the cluster by updating the `maintenance` list on the `CassandraCluster` resource. Each maintenance request has two fields: `dc` and `pods`.
The `dc` field (required) is the name of the dc and the `pods` field (optional) is a list of pod names to put in maintenance mode. If the `pods` field is not specified, every pod in the `dc` is put into maintenance mode.

Here is an example of enabling maintenance mode for a single pod:
```yaml
maintenance:
  - dc: dc1
    pods: [example-cluster-cassandra-dc1-0]
```

Enable maintenance mode for an entire dc:
```yaml
maintenance:
  - dc: dc1
```

Enable maintenance mode for multiple pods in the same dc:
```yaml
maintenance:
  - dc: dc1
    pods: [example-cluster-cassandra-dc1-0, example-cluster-cassandra-dc1-1]
```

Enable maintenance mode for pods in the different dcs:
```yaml
maintenance:
  - dc: dc1
    pods: [example-cluster-cassandra-dc1-0]
  - dc: dc2
    pods: [example-cluster-cassandra-dc2-0]
```

When you are done debugging a Cassandra pod, you may take it out of maintenance mode by removing it from the `maintenance` list and reapplying the resource. Here is an example of disabling maintenance mode for a single pod:

Suppose you have applied this configuration:
```yaml
maintenance:
  - dc: dc1
    pods: [example-cluster-cassandra-dc1-0, example-cluster-cassandra-dc1-1]
```

To take pod `example-cluster-cassandra-dc1-0` out of maintenance mode, simply remove it
```yaml
maintenance:
  - dc: dc1
    pods: [example-cluster-cassandra-dc1-1]
```

and reapply the updated `CassandraCluster` manifest:

```bash
kubectl apply -f cassandracluster.yaml
```

To take all pods out of maintenance mode, remove the maintenance object from the CR and reapply.

