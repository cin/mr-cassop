---
title: Quickstart
slug: /quickstart
---

## Prerequisites

* Kubernetes 1.28+
* Helm 3.15+
* kubectl configured to communicate with your cluster

## Install mr-cassop

Released mr-cassop images are published to GitHub Container Registry under `ghcr.io/cin/mr-cassop`, and each release attaches a Helm chart archive.

Set the release you want to install:

```bash
export MR_CASSOP_VERSION=0.6.0
```

Download the chart and create the operator namespace:

```bash
curl -LO "https://github.com/cin/mr-cassop/releases/download/${MR_CASSOP_VERSION}/mr-cassop-${MR_CASSOP_VERSION}.tgz"
kubectl create namespace mr-cassop-system
```

Install the operator:

```bash
helm install mr-cassop "./mr-cassop-${MR_CASSOP_VERSION}.tgz" -n mr-cassop-system
```

You should see your operator pod up and running:

```bash
kubectl get pods --namespace mr-cassop-system

NAME                         READY   STATUS    RESTARTS   AGE
mr-cassop-56997bfc5c-gz788   1/1     Running   0          40s
```

### Local Development Install

If you are developing locally, build images and install from the checked-out chart with `local-values.yaml`:

```bash
VERSION=dev ./build-images.sh
kubectl create namespace mr-cassop-system
helm install mr-cassop ./mr-cassop -n mr-cassop-system -f local-values.yaml
```

For more build options, see the [Development Guide](development.md) and [Docker Build Documentation](https://github.com/cin/mr-cassop/blob/main/DOCKER_BUILD.md).

## Create Required Secrets

### 1. Create Application Namespace

```bash
kubectl create namespace cassop
```

### 2. Admin Role Secret

The operator needs a secret containing the admin role credentials used for Cassandra management.

Do not use the example password in a real environment.

```bash
kubectl create secret generic admin-secret \
  --from-literal=admin-role=admin \
  --from-literal=admin-password=admin123 \
  -n cassop
```

### 3. Image Pull Secret

`imagePullSecretName` is part of the `CassandraCluster` spec. For public GHCR images, a minimal placeholder secret is enough:

```bash
kubectl create secret generic test-secret \
  --from-literal=.dockerconfigjson='{}' \
  --type=kubernetes.io/dockerconfigjson \
  -n cassop
```

If you override the default images with a private registry, create this as a real registry pull secret instead.

## Deploy CassandraCluster

Use the image pull secret and admin role secret created above to deploy a 3-node Cassandra cluster:

```bash
kubectl apply -f - <<EOF
apiVersion: db.ibm.com/v1alpha1
kind: CassandraCluster
metadata:
  name: example
  namespace: cassop
spec:
  imagePullSecretName: test-secret
  adminRoleSecretName: admin-secret
  dcs:
  - name: dc1
    replicas: 3
  cassandra:
    persistence:
      enabled: false
    resources:
      requests:
        cpu: 750m
        memory: 1536Mi
      limits:
        cpu: "2"
        memory: 2Gi
    jvmOptions:
    - -Xmx1024M
    - -Xms1024M
EOF
```

**Note: you must define at least one DC.**

The resource values above are intended for a small demo cluster. Cassandra can start with less, but under constrained CPU it may fail liveness checks during bootstrap. Use persistent storage and size the pods for your workload before using this outside a demo environment.

Check the deployment progress:

```bash
kubectl get cassandraclusters -n cassop
kubectl get pods -n cassop
```

Wait until all Cassandra pods are `2/2 Running`. You should see services created:

```bash
kubectl get svc -n cassop

NAME                    TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)                              AGE
example-cassandra-dc1   ClusterIP   None         <none>        7000/TCP,7001/TCP,7199/TCP,9042/TCP   3m
```

Now you can execute queries:

```bash
kubectl exec -it example-cassandra-dc1-0 -n cassop -c cassandra -- \
  cqlsh -u admin -p admin123 -e "DESCRIBE keyspaces;"

system_traces  system_schema  system_auth  system  system_distributed  reaper
```

You can also inspect cluster health with `nodetool`:

```bash
kubectl exec -it example-cassandra-dc1-0 -n cassop -c cassandra -- bash -c \
  'nodetool -u "$(cat /etc/cassandra-auth-config/admin-role)" -pw "$(cat /etc/cassandra-auth-config/admin-password)" status'
```

Healthy nodes show `UN` (`Up`/`Normal`):

```text
Datacenter: dc1
===============
Status=Up/Down
|/ State=Normal/Leaving/Joining/Moving
--  Address      Load        Tokens  Owns (effective)  Host ID                               Rack
UN  10.244.1.16  116.9 KiB   16      100.0%            aa08a631-1800-44c3-9cba-e23507a6e43f  rack1
UN  10.244.3.8   114.45 KiB  16      100.0%            ae5cfa8c-43e9-4112-9aef-d4911c27599d  rack1
UN  10.244.2.8   97.3 KiB    16      100.0%            6f422689-2995-42a1-a0d0-39b6a1ba6b16  rack1
```

See the [CassandraCluster field specification reference](cassandracluster-configuration.md) for more details.

## Scaling the Cluster

You can scale your cluster by updating the replicas:

```bash
kubectl patch cassandracluster example -n cassop --type='merge' -p='{"spec":{"dcs":[{"name":"dc1","replicas":5}]}}'
```

Or edit the cluster directly:

```bash
kubectl edit cassandracluster example -n cassop
# Change replicas: 3 to replicas: 5
```

Watch the scaling process:

```bash
kubectl get pods -n cassop -w
```

## Uninstall mr-cassop and the Cluster

```bash
# Delete the cluster
kubectl delete cassandracluster example -n cassop

# Uninstall the operator
helm uninstall mr-cassop -n mr-cassop-system

# Delete namespaces
kubectl delete namespace cassop
kubectl delete namespace mr-cassop-system

# Delete CRDs
kubectl delete crd cassandraclusters.db.ibm.com cassandrabackups.db.ibm.com cassandrarestores.db.ibm.com
```
