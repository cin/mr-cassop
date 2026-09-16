---
title: Upgrading to Cassandra 5.0
slug: /upgrading-to-cassandra-5
---

This page covers the operator's Cassandra 4.1.x -> 5.0.x upgrade path, introduced with the `0.7.0` release line.
It's based on a real rolling upgrade of a production cluster from 4.1.12 to 5.0.9, not just a fresh smoke-test cluster.

## Versioning model

- `0.6.x` is the last release line targeting Cassandra 4.1.x.
- `0.7.0` opens the Cassandra 5.0.x-targeting line.
- There are no standing per-major-version maintenance branches. If a `4.x` patch is ever needed after `main` has
  moved on to `0.7.x`+, it branches off the last `0.6.x` tag (`0.6.6`) at that time.

## The config ConfigMap is shared across every managed cluster

`api/v1alpha1` has no `CassandraVersion` field -- the operator has no per-`CassandraCluster` notion of which
Cassandra major version a cluster is running, only whatever image tag `.spec.cassandra.image` points at.
The vendored `cassandra.yaml` is a single file, sourced by the chart into one `OperatorCassandraConfigCM`, and
merged into the config of **every** `CassandraCluster` the operator manages, regardless of what image tag that
cluster actually runs.

That matters because Cassandra's config loader hard-errors on unrecognized top-level keys
(`Invalid yaml. Please remove properties ...`). Once the operator chart is upgraded to a version whose vendored
`cassandra.yaml` targets 5.0 (with new 5.0-only keys like `storage_compatibility_mode` or `cidr_authorizer`), any
`CassandraCluster` still pinned to a 4.1.x image will crash-loop the next time one of its pods restarts and picks
up that config.

:::caution Bump the CR image and the operator chart together

Never leave a gap where the new chart version's config ConfigMap is live but a `CassandraCluster`'s
`.spec.cassandra.image` still points at a 4.1.x image. Bump `.spec.cassandra.image` to a 5.0.x image at the same
time as, or immediately before, the `helm upgrade` of the operator chart to `0.7.x`+.

This is the one case where the general [operator upgrade](operator-upgrade.md) guidance that "the order of
invocations is not important" doesn't hold -- for a Cassandra major-version jump, order matters.

:::

## Rolling `storage_compatibility_mode` forward

The vendored `cassandra.yaml` in the `0.7.x` line leaves `storage_compatibility_mode` at Cassandra 5's own default,
`CASSANDRA_4`. That default is intentional: it keeps a rolling 4.1 -> 5.0 upgrade safe while old and new nodes
briefly coexist in the same ring.

There's no dedicated CRD field for `storage_compatibility_mode` (see [#140](https://github.com/cin/mr-cassop/issues/140)),
so walking it forward after the upgrade is done through `.spec.cassandra.configOverrides`:

1. Upgrade every node's image to 5.0.x first, with `storage_compatibility_mode` left at `CASSANDRA_4` (the
   vendored default -- no override needed yet).
2. Once every node in every DC is confirmed running the 5.0.x image, set:

   ```yaml
   spec:
     cassandra:
       configOverrides: |
         storage_compatibility_mode: UPGRADING
   ```

   This triggers a rolling restart. New Cassandra-5-only features stay disabled until every node has restarted
   into `UPGRADING` mode.
3. Once that rollout completes, move to `NONE`:

   ```yaml
   spec:
     cassandra:
       configOverrides: |
         storage_compatibility_mode: NONE
   ```

   This eliminates the ongoing cost of checking node versions and is the steady-state setting for an all-5.0.x
   cluster. Don't skip straight to `NONE` from `CASSANDRA_4` -- if a node ends up back on the old version by
   accident, `NONE` mode no longer toggles behaviors the way `UPGRADING` mode does.

## Monitoring agent

The `datastax` and `instaclustr` Cassandra monitoring agents were dropped as of
[#139](https://github.com/cin/mr-cassop/issues/139) -- neither has a known-good Cassandra 5 path. `tlp`
(`jmx_prometheus_javaagent`) is now the only valid value for `.spec.cassandra.monitoring.agent`.

A `CassandraCluster` still configured for `datastax` or `instaclustr` will fail CRD validation against the
`0.7.x` CRD's narrower enum -- update `.spec.cassandra.monitoring.agent` to `tlp` before or as part of the
upgrade.

## What to expect during the rolling upgrade

This is what was actually observed rolling a live cluster from 4.1.12 to 5.0.9:

- The StatefulSet rolls one pod at a time on its own once the CR and chart changes are applied -- no manual
  `kubectl rollout restart` is needed or should be used.
- Each new-version node reads its existing 4.1.x-written sstables directly on boot. No separate migration or
  upgrade-sstables step is required for the cluster to come up.
- Transient `cluster schema versions not consistent` messages and connection-timeout errors in the operator log
  during the rollout are expected. They self-heal once the ring stabilizes; they are not a sign of a stuck
  upgrade on their own.
- Reaper briefly fails to reach the cluster mid-rollout and recovers on its own once nodes stabilize. No Reaper
  restart is needed.

## Backup/restore compatibility

Icarus/backup compatibility across the 4.1 -> 5.0 transition is tracked separately in
[#145](https://github.com/cin/mr-cassop/issues/145) and hadn't been verified as of this writing. Don't assume
backup/restore is upgrade-safe across the transition until that's confirmed.
