---
title: Cassandra backup and restore
slug: /cassandra-backup-restore
---

mr-cassop uses [Icarus](https://github.com/instaclustr/icarus) to perform backups and restores. 
Refer to its documentation for more details on how the backup and restore procedures work internally.

Backup and restore creation and configuration is done by creating CassandraBackup and CassandraRestore custom resources.

S3, Azure and GCP storage providers are supported (as well as S3-compatible Minio/Oracle/Ceph endpoints).
The type is determined by the `storageLocation` field, which should be in the following format:
`protocol://backup/location`. So an S3 provider would look like to following: `s3://location/to/the/backup`

### Credentials

Icarus resolves cloud credentials the same way the underlying cloud SDK does -- there is no
mr-cassop- or Icarus-specific credential injection mechanism. In practice this means:

- **AWS S3** -- the AWS SDK's standard credential chain. On EKS, the recommended approach is
  [IAM Roles for Service Accounts (IRSA)](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html):
  set `spec.cassandra.serviceAccountAnnotations` on the `CassandraCluster` to annotate the
  ServiceAccount shared by the cassandra pod and its Icarus sidecar with the IAM role to assume,
  e.g.:

  ```yaml
  spec:
    cassandra:
      serviceAccountAnnotations:
        eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/my-backup-role
  ```

  EKS's webhook injects the necessary environment/token into the pod, and the AWS SDK picks up
  short-lived credentials automatically -- no static keys, no Secret needed for this path.
- **Azure** / **GCP** -- similarly, the equivalent workload-identity mechanisms
  ([Azure Workload Identity](https://learn.microsoft.com/en-us/azure/aks/workload-identity-overview),
  [GCP Workload Identity](https://cloud.google.com/kubernetes-engine/docs/concepts/workload-identity))
  are the supported path, configured the same way via `serviceAccountAnnotations`.

**`secretName`** (referencing a Secret with `awsaccesskeyid`/`awssecretaccesskey`/`awsregion`/etc.
keys) is still accepted on `CassandraBackup`/`CassandraRestore`, but as of the currently pinned
Icarus/esop version it is **not functional** for any cloud provider -- Icarus stopped reading a
K8s Secret for credentials when esop moved off its old AWS SDK v1 code path (see
[cin/icarus#1](https://github.com/cin/icarus/pull/1) if you're chasing the history). Don't rely
on it; use workload identity instead.

#### Local / non-cloud development clusters

There is currently no supported way to exercise cloud-backed backup/restore against a local
cluster (kind, minikube, etc.) that lacks workload-identity support -- `storageLocation` also
rejects the `file://` protocol outright (and it wouldn't coordinate correctly across a
multi-node cluster's separate pod disks even if it didn't), so there's no "just point it at local
disk" fallback either. If you need to exercise this locally, the closest option today is
`exec`-ing into a running Cassandra pod's `icarus` container and setting `AWS_ACCESS_KEY_ID` /
`AWS_SECRET_ACCESS_KEY` / `AWS_REGION` as process environment variables before triggering a
backup -- a manual, one-off workaround, not something the CR or operator manages for you.

### CassandraBackup

To create a backup simply create a CassandraBackup resource:

```yaml
apiVersion: db.ibm.com/v1alpha1
kind: CassandraBackup
metadata:
  name: example-backup
spec:
  cassandraCluster: test-cluster
  storageLocation: s3://bucket-name/backup/location
  secretName: backup-restore-credentials
```

To track progress of the backup process you can see the status of the object, where you can see the state, progress and other information about the backup. If a backup failed you'll see the errors in the status object as well.

See [all fields description](cassandrabackup-configuration.md) for more information

#### Restarting a failed backup

If a misconfigured backup has failed, the operator will retry only when a configuration is changed. If a retry is needed without a configuration change, simply recreate the resource.

### CassandraRestore

To restore a backup a CassandraRestore should be created which will start the restore process.

The backup can be referenced either by setting the corresponding CassandraBackup resource name or by manually setting the `storageLocation` and `snapshotTag` fields.

```yaml
apiVersion: db.ibm.com/v1alpha1
kind: CassandraRestore
metadata:
  name: rest3
spec:
    cassandraCluster: test-cluster
    cassandraBackup: example-backup
    # or the following if no corresponding cassandraBackup available
    # storageLocation: s3://bucket-name/backup/location
    # snapshotTag: example-backup //the name of the CassandraBackup if the backup was created using the mr-cassop
```

mr-cassop will update the progress of the restore in the status field of CassandraRestores CR object.

See [all fields description](cassandrarestore-configuration.md) for more information.