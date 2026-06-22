---
title: Sysctl configuration
slug: /sysctl
---

In order for Cassandra to operate correctly, mr-cassop sets some [`sysctl`](https://kubernetes.io/docs/tasks/administer-cluster/sysctl-cluster/) parameters by default. The defaults follow the [Apache Cassandra 4.1 recommended production settings](https://cassandra.apache.org/doc/4.1/cassandra/managing/operating/hardware.html).

They can be overriden by setting the `.spec.cassandra.sysctls` map in the following way:

```yaml
apiVersion: db.ibm.com/v1alpha1
kind: CassandraCluster
metadata:
  name: test-cluster
spec:
  cassandra:
    sysctls:
      net.ipv4.ip_local_port_range: "1025 65530"
      net.ipv4.tcp_rmem: "4096 87380 16777216"
      net.ipv4.tcp_wmem: "4096 65536 16777216"
```

Default values:

| Name                         | Default Value       | Purpose                                                              |
|------------------------------|---------------------|---------------------------------------------------------------------|
| net.ipv4.ip_local_port_range | 1025 65535          | Widen the ephemeral port range for many client/internode connections |
| net.ipv4.tcp_rmem            | 4096 87380 16777216 | Per-socket TCP read buffer (min/default/max)                        |
| net.ipv4.tcp_wmem            | 4096 65536 16777216 | Per-socket TCP write buffer (min/default/max)                       |
| net.core.rmem_max            | 16777216            | Upper bound for socket read buffers; uncaps `tcp_rmem` max          |
| net.core.wmem_max            | 16777216            | Upper bound for socket write buffers; uncaps `tcp_wmem` max         |
| net.core.rmem_default        | 16777216            | Default socket read buffer size                                     |
| net.core.wmem_default        | 16777216            | Default socket write buffer size                                    |
| net.core.optmem_max          | 40960               | Max ancillary buffer size per socket                               |
| net.core.somaxconn           | 65535               | Max queued connections per listening socket                        |
| vm.dirty_background_bytes    | 10485760            | Start background writeback early to smooth I/O latency             |
| vm.dirty_bytes               | 1073741824          | Cap dirty page memory before forcing synchronous writeback         |
| vm.max_map_count             | 1048575             | Allow enough memory-map areas for Cassandra's mmap'd SSTables      |
| vm.swappiness                | 1                   | Minimize swapping of the JVM heap                                  |

:::note Changes from earlier releases

Starting with the Cassandra 4.1 target, `vm.max_map_count` was corrected from `1073741824` to the upstream-recommended `1048575`, the `net.core.*` buffer maxes were added (without them the `tcp_rmem`/`tcp_wmem` 16 MiB max can never be reached), and the no-op/legacy `fs.file-max`, `net.ipv4.tcp_ecn`, and `net.ipv4.tcp_window_scaling` entries were removed. Any value you set explicitly in `.spec.cassandra.sysctls` is still applied as-is, including keys that are no longer defaults.

:::

:::caution Namespaced vs. node-level sysctls

mr-cassop applies these via a privileged init container running `sysctl -w`. Network sysctls (`net.*`, including `net.core.somaxconn`) are namespaced and only affect the pod. Virtual-memory sysctls (`vm.*`) are **not** namespaced and are written to the **host node** kernel, affecting every pod on that node. This is safe on nodes dedicated to Cassandra; on shared nodes, prefer node-level tuning (e.g. a tuning DaemonSet or node bootstrap) and trim the `vm.*` overrides accordingly.

:::

## Not handled by sysctls

Two important OS settings are **not** kernel sysctls and are not managed through `.spec.cassandra.sysctls`:

- **File descriptor / memlock limits.** Cassandra's real file-handle constraint is the per-process `nofile` ulimit (and `memlock` for off-heap/mmap), set via the container runtime / `securityContext`, not `fs.file-max`. The system-wide `fs.file-max` is auto-sized from RAM on modern kernels and is no longer set by default.
- **Transparent Huge Pages (THP).** Upstream recommends disabling THP, which is configured under `/sys/kernel/mm/transparent_hugepage/` at the node level rather than via `sysctl`.
