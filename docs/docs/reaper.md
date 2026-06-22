---
title: Reaper
slug: /reaper
---

mr-cassop deploys [Cassandra Reaper](http://cassandra-reaper.io/) for each managed `CassandraCluster`. Reaper coordinates Cassandra repairs and exposes a web UI/API for repair visibility and control.

The operator registers the Cassandra cluster with Reaper and configures Reaper to authenticate to Cassandra/JMX using the cluster's generated management credentials. The default Reaper image is `thelastpickle/cassandra-reaper:4.2.5`; override `.spec.reaper.image` in a `CassandraCluster` or the chart-level `reaperImage` value if you need a different image.

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

### Monitoring

Reaper metrics are reported by default via the Dropwizard Metrics interface. These metrics are accessible on Reaper's admin port under the `/prometheusMetrics` route. If you would like Prometheus to scrape these metrics, enable the Reaper service monitor by setting `reaper.serviceMonitor.enabled` to `true`. This creates a ServiceMonitor for Reaper in the same namespace as your Cassandra cluster unless you override the namespace. You may also specify additional properties such as `labels` and `scrapeInterval`. See the [CassandraCluster field specification reference](cassandracluster-configuration.md) for more information on these fields.