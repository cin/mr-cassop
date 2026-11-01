---
title: Reaper
slug: /reaper
---

mr-cassop deploys [Cassandra Reaper](http://cassandra-reaper.io/) for each managed `CassandraCluster`. Reaper coordinates Cassandra repairs and exposes a web UI/API for repair visibility and control.

The operator registers the Cassandra cluster with Reaper and configures Reaper to authenticate to Cassandra/JMX using the cluster's generated management credentials. The default Reaper image is `thelastpickle/cassandra-reaper:5.0.1`; override `.spec.reaper.image` in a `CassandraCluster` or the chart-level `reaperImage` value if you need a different image.

### Schedule Repairs

The `reaper` object contains an optional `repairSchedules` field. This field defines Reaper scheduled repairs. When the `CassandraCluster` CRD is reconciled, mr-cassop creates, updates, or removes Reaper repair schedules to match the configuration.

The fields used to configure a repair intentionally match Reaper's `POST /repair_schedule` API where practical. Some combinations are not valid in Reaper. For example, `repairParallelism` must be `PARALLEL` when `incrementalRepair` is `true`; the admission webhook validates this.

More information can be found through reaper's [API](http://cassandra-reaper.io/docs/api/) and [reaper specific](http://cassandra-reaper.io/docs/configuration/reaper_specific/) documentation.

It's also important to consider whether you want to repair TWCS or DTCS tables. It is **strongly** advised not to repair these tables, so TWCS tables are blacklisted by default. If you want to enable repairs on these types of tables, you can specify `reaper.blacklistTWCS: false` and `REAPER_BLACKLIST_TWCS` will be set accordingly.

See [Reaper Repairs Configuration](reaper-repairs-configuration.md) for a list of fields that can be configured. This is lifted directly from Reaper's [API](http://cassandra-reaper.io/docs/api/) documentation with the options the chart doesn't support removed.

Here's an example of a weekly, datacenter-aware repair on the `counter1` table of `keyspace1` that will use 2 threads:
```yaml
reaper:
  repairSchedules:
    enabled: true
    repairs:
      - keyspace: keyspace1
        tables: [counter1]
        scheduleDaysBetween: 7
        scheduleTriggerTime: "2020-09-08T04:00:00"
        datacenters: [dc1]
        repairThreadCount: 2
        intensity: "0.75"
        repairParallelism: "DATACENTER_AWARE"
```

Here's another example of a repair that occurs weekly but will repair all the tables in the keyspace and across all DCs.
```yaml
reaper:
  repairSchedules:
    enabled: true
    repairs:
      - keyspace: system_auth
        scheduleDaysBetween: 7
        scheduleTriggerTime: "2021-01-06T04:00:00"
        repairThreadCount: 4
        repairParallelism: "PARALLEL"
```

### Repairs during a rolling update

A repair needs every replica of a range. A repair that is running while the operator restarts the Cassandra pods one after another stalls, postpones its segments and adds load to the nodes that are still up. Set `reaper.pauseRepairsDuringRoll: true` to have the operator pause repairs around such a rolling update. It is off by default.

When the operator is about to change the pod template of a Cassandra statefulset, which is what a config change or an operator upgrade does, it:

1. pauses every repair run that is `RUNNING` and every repair schedule that is `ACTIVE` in Reaper, whoever created it, and records their IDs in `status.pausedRepairs` first, so a crash can't leave repairs paused with no record of them;
2. lets the statefulset roll, and meanwhile neither starts repairs of its own nor touches Reaper's cluster registration;
3. once every Cassandra statefulset has finished rolling and is ready, resumes exactly the runs and schedules it recorded, and clears the record.

The operator reports `RepairsPaused` and `RepairsResumed` events. Things to know:

* Only a pod template change made by the operator is covered. Node drains, `kubectl rollout restart`, scaling, maintenance mode and crashing pods do not pause repairs.
* Pausing a run stops new segments from starting. Segments that are already running finish.
* A run or schedule you paused yourself is never resumed by the operator. One that is no longer paused when the roll ends, because you resumed or deleted it, is left alone.
* If Reaper can't be reached or a repair can't be paused, the operator reports a `RepairsPauseFailed` event and carries on with the roll.
* If Reaper loses its repair runs and schedules by other means before the roll is over, for example when the operator re-initializes Reaper after losing contact with the nodes, there is nothing left to resume: the operator clears its record and reports in the `RepairsResumed` event how many it could resume.
* The repairs stay paused for as long as the roll takes. Turning the option off during a roll does not strand them: they are still resumed afterwards.

### Monitoring

Reaper metrics are reported by default via the Dropwizard Metrics interface. These metrics are accessible on Reaper's admin port under the `/prometheusMetrics` route. If you would like Prometheus to scrape these metrics, enable the Reaper service monitor by setting `reaper.serviceMonitor.enabled` to `true`. This creates a ServiceMonitor for Reaper in the same namespace as your Cassandra cluster unless you override the namespace. You may also specify additional properties such as `labels` and `scrapeInterval`. See the [CassandraCluster field specification reference](cassandracluster-configuration.md) for more information on these fields.