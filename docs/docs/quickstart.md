---
title: Quickstart
slug: /quickstart
---

## Prerequisites

* Kubernetes 1.19+
* Helm
* kubectl configured to communicate with your cluster

## Build Images

For local development, you'll need to build the Docker images. Choose based on your needs:

```bash
# Core images only (fastest - operator + prober)
./build-local.sh

# Essential images (complete local development - + cassandra)
./build-local.sh --cassandra
```

> 💡 **For comprehensive build options**, see the [Development Guide](development.md) and [Docker Build Documentation](../../DOCKER_BUILD.md).

## Install mr-cassop

### 1. Create Namespace

```bash
kubectl create namespace mr-cassop-system
```

### 2. Install via Helm

The operator is installed using the local Helm chart:

```bash
helm install mr-cassop ./mr-cassop -n mr-cassop-system -f local-values.yaml
```

You should see your operator pod up and running:

```bash
kubectl get pods --namespace mr-cassop-system

NAME                         READY   STATUS    RESTARTS   AGE
mr-cassop-56997bfc5c-gz788   1/1     Running   0          40s
```

## Create Required Secrets

### 1. Create Application Namespace

```bash
kubectl create namespace cassop
```

### 2. Admin Role Secret

The operator needs a secret containing the admin role credentials used for Cassandra management.

> Don't forget to replace `admin-password=admin123` with your secure password

```bash
kubectl create secret generic admin-secret \
  --from-literal=admin-role=admin \
  --from-literal=admin-password=admin123 \
  -n cassop
```

### 3. Image Pull Secret

For local development, create a minimal image pull secret:

```bash
kubectl create secret generic test-secret \
  --from-literal=.dockerconfigjson='{}' \
  --type=kubernetes.io/dockerconfigjson \
  -n cassop
```

## Deploy CassandraCluster

Use the image pull secret and admin role secret created before to deploy the cluster:

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
        cpu: 500m
        memory: 1Gi
      limits:
        cpu: 500m
        memory: 1Gi
EOF
```

**Note: you must define at least one DC.**

Check the deployment progress:

```bash
kubectl get cassandraclusters -n cassop
kubectl get pods -n cassop
```

Wait until the cluster is up and running. You'll see services created:

```bash
kubectl get svc -n cassop

NAME                    TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)                                        AGE
example-cassandra-dc1   ClusterIP   None         <none>        7000/TCP,7001/TCP,7199/TCP,9042/TCP,9160/TCP   3m
```

Now you can execute queries:

```bash
kubectl exec -it example-cassandra-dc1-0 -n cassop -c cassandra -- \
  cqlsh -u admin -p admin123 -e "DESCRIBE keyspaces;"

system_traces  system_schema  system_auth  system  system_distributed
```

You can also execute `cqlsh` and `nodetool` commands:

```bash
kubectl exec -it example-cassandra-dc1-0 -n cassop -c cassandra -- bash
cassandra@example-cassandra-dc1-0:~$ cqlsh -e "DESCRIBE keyspaces;"

system_schema  system_auth  system  reaper  system_distributed  system_traces

cassandra@example-cassandra-dc1-0:~$ nodetool status
Datacenter: dc1
===============
Status=Up/Down
|/ State=Normal/Leaving/Joining/Moving
--  Address         Load       Tokens       Owns (effective)  Host ID                               Rack
UN  172.30.200.204  919.86 KiB  16           100.0%            01b26cc1-4870-4617-97ab-adfa566cccee  rack1
UN  172.30.16.197   926.94 KiB  16           100.0%            f3a861ae-848d-4e52-a7bf-dc63cb87ef57  rack1
UN  172.30.200.83   924.49 KiB  16           100.0%            dd93c221-a8b1-47fd-aa63-40282863bf57  rack1
```

See the [CassandraCluster field specification reference](cassandracluster-configuration.md) for more details.

## Scaling the Cluster

You can easily scale your cluster by updating the replicas:

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
