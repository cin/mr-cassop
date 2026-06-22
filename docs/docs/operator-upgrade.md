---
title: Operator Upgrade
slug: /operator-upgrade
---

The operator upgrade is performed with help of Helm and manual CRD upgrade.

The CRD is have to be updated manually with `kubectl apply -f ...` or some other mechanism as Helm [does not support CRD upgrades](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/). 

The Helm upgrade part is no different from any other upgrades. Simply run `helm upgrade --version <chart_version> <chart path>`

The order of invocations is not important.

:::caution

Every operator upgrade change configs for CassandraClusters which triggers a rolling upgrade of CassandraClusters.
Plan your upgrade accordingly.

:::

Depending on the change in the operator, additional steps may be required during upgrade.
Version specific upgrade instructions can be found in the release notes.

## Notable changes

### Default `sysctls` updated for Cassandra 4.1

The default [`sysctl`](sysctl.md) values were aligned with the Apache Cassandra 4.1 recommended production settings. Clusters that never set `.spec.cassandra.sysctls` will pick up the new defaults on the next reconcile, which restarts the pods (see the rolling-upgrade caution above). Changes:

- `vm.max_map_count` corrected from `1073741824` to `1048575` (the upstream recommendation).
- Added `net.core.rmem_max`, `net.core.wmem_max`, `net.core.rmem_default`, `net.core.wmem_default`, and `net.core.optmem_max` (`16777216`/`40960`). Without these the `tcp_rmem`/`tcp_wmem` 16 MiB maximums could never be reached.
- `net.core.somaxconn` aligned to `65535`.
- Removed the legacy/no-op defaults `fs.file-max`, `net.ipv4.tcp_ecn`, and `net.ipv4.tcp_window_scaling`.

Any keys you set explicitly in `.spec.cassandra.sysctls` are still applied as-is, including ones that are no longer defaults. If you relied on the old `vm.max_map_count`/`fs.file-max` values, set them explicitly before upgrading.