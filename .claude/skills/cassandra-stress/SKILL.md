---
name: cassandra-stress
description: Run cassandra-stress load-test Jobs (write and/or read) against an already-installed, ready CassandraCluster in the local "cassandra" kind cluster, using pluggable stress-profile YAMLs for different data models and read/write patterns. Ships with an IoT-style time-series "device_data" profile; add more under profiles/. Not for bringing a cluster up — see the local-install skill for that.
---

# mr-cassop cassandra-stress load testing

Runs `cassandra-stress` as a one-off Kubernetes `Job` against an already-running `CassandraCluster` (see the `local-install` skill to bring one up first). Supports the built-in `keyspace1.standard1` schema for a quick smoke load, and custom stress-profile YAMLs under `profiles/` for specific data models and read/write patterns.

## Inputs

- `CR_NAME` — CassandraCluster name (default `test-cluster`).
- `NAMESPACE` — (default `cassop`).
- `DC` — datacenter name, used to build the target service name and the default schema's replication (default `dc1`).
- `RF` — replication factor for the built-in `keyspace1` schema (default `3`). Custom profiles set their own in `keyspace_definition`.
- `CL` — consistency level for every operation (default `LOCAL_QUORUM`). cassandra-stress's own default is `LOCAL_ONE`, which hides the cost of cross-replica coordination and doesn't reflect a real RF=3 workload.
- `PROFILE` — `default` for the built-in `keyspace1.standard1` schema (no custom YAML needed), or the name of a file under `profiles/` (without `.yaml`), or a path to your own profile YAML. Default `device-data`.
- `OPERATION` — what to run:
  - `PROFILE=default`: `write` or `read`.
  - custom profile: `insert` for the profile's `insert:` section, or the name of a query defined under that profile's `queries:` section (e.g. `by_device_range` for `device-data`).
  Required.
- One of:
  - `N` — fixed op count, e.g. `2000000`.
  - `DURATION` — time-bounded run, e.g. `90m`.
- `THREADS` — client thread count (default `4`).
- `TARGET_RATE` — optional throttle in ops/s, becomes `fixed=<TARGET_RATE>/s`. Omit for unthrottled (cassandra-stress runs at max speed) — fine for a short smoke test, but easily saturates a 3-node kind cluster on a laptop; throttle anything longer than a couple of minutes.
- `IMAGE_TAG` — cassandra image tag to run the stress client from (default `dev`). Must already be loaded into the kind cluster (`imagePullPolicy: Never`), and `dev` often isn't — check what's actually there first with `docker exec cassandra-control-plane crictl images | grep mr-cassop/cassandra` and use one of the tags it lists. If the tag you want is missing, run `kind load docker-image --name cassandra ghcr.io/cin/mr-cassop/cassandra:<IMAGE_TAG>` first (see the `local-install` skill).

Confirm `PROFILE`, `OPERATION`, and `N`/`DURATION` with the user before running if not already given.

## Pre-flight

```bash
kubectl get cassandraclusters.db.ibm.com -n <NAMESPACE> <CR_NAME> -o jsonpath='{.status.ready}'
kubectl get secret admin-secret -n <NAMESPACE> >/dev/null
```

Stop if the CR isn't `true`-ready or `admin-secret` doesn't exist — getting the cluster into that state is the `local-install` skill's job, not this one's.

### Enable monitoring if prometheus-operator is present

`local-install` always installs the prometheus-operator stack, and with `local-values.yaml` the chart ships all four dashboard sets — `tlp-*` (overview, write-path, read-path, jvm-overview, client-connections), `prober-overview`, `reaper-overview` and the operator's own `mr-cassop` dashboard. The ConfigMaps are there from install (`kubectl get cm -A -l grafana_dashboard=1`), but every one of them stays **empty until the matching `ServiceMonitor` exists and Prometheus actually selects it**. Each of the four has its own switch, and three of the four are off by default. Without them a stress run produces no Cassandra-side metrics at all — only generic pod CPU from cAdvisor.

Two things are needed for each source:

1. The `ServiceMonitor` has to exist — a per-component `serviceMonitor.enabled: true`, not just `monitoring.enabled`.
2. It has to carry the label Prometheus selects on. Check it, don't assume: `kubectl get prometheus -A -o jsonpath='{..serviceMonitorSelector}'` — for the kube-prometheus-stack install that's `release: prometheus-operator`. A `ServiceMonitor` without that label is simply ignored, silently.

| Source | Switch | Dashboards |
| --- | --- | --- |
| Cassandra | `spec.cassandra.monitoring.{enabled,agent,serviceMonitor}` on the CR | `tlp-*` |
| Prober | `spec.prober.serviceMonitor` on the CR | `prober-overview` |
| Reaper | `spec.reaper.serviceMonitor` on the CR | `reaper-overview` |
| Operator | chart's `monitoring.serviceMonitor.labels` in `local-values.yaml` | `mr-cassop` |

Check and fix this **before** step 1, never mid-run:

```bash
if helm status prometheus-operator -n prometheus-operator >/dev/null 2>&1; then
  MONITORING_ENABLED=$(kubectl get cassandraclusters.db.ibm.com -n <NAMESPACE> <CR_NAME> -o jsonpath='{.spec.cassandra.monitoring.enabled}')
  if [ "$MONITORING_ENABLED" != "true" ]; then
    echo "prometheus-operator is installed but monitoring is off on <CR_NAME> — enabling it before the run."
    kubectl patch cassandraclusters.db.ibm.com -n <NAMESPACE> <CR_NAME> --type merge -p \
      '{"spec":{"cassandra":{"monitoring":{"enabled":true,"agent":"tlp","serviceMonitor":{"enabled":true,"labels":{"release":"prometheus-operator"}}}}}}'
    for sts in $(kubectl get statefulset -n <NAMESPACE> -l cassandra-cluster-instance=<CR_NAME> -o name); do
      kubectl rollout status "$sts" -n <NAMESPACE> --timeout=600s
    done
  fi

  # Prober and Reaper: ServiceMonitor objects only, no pod restarts.
  kubectl patch cassandraclusters.db.ibm.com -n <NAMESPACE> <CR_NAME> --type merge -p \
    '{"spec":{"prober":{"serviceMonitor":{"enabled":true,"labels":{"release":"prometheus-operator"}}},"reaper":{"serviceMonitor":{"enabled":true,"labels":{"release":"prometheus-operator"}}}}}'

  # Operator: the chart sets labels to {operator: mr-cassop} only, which Prometheus doesn't select.
  kubectl label servicemonitor -n mr-cassop-system mr-cassop-metrics release=prometheus-operator --overwrite
fi
```

`agent: tlp` is the only valid value (`controllers/cassandra_container.go:116`) and is what the CR defaults to, but set it explicitly so the intent is on the page. `monitoring.enabled: true` on its own adds the agent and rolls the pods, yet creates no `ServiceMonitor` — that needs `monitoring.serviceMonitor.enabled: true` as well (`controllers/cassandra_service_monitor.go:49`). Getting that wrong is easy to miss: the pods restart, everything looks like it worked, and the dashboards stay blank.

**The Cassandra patch rolls every Cassandra pod once.** The tlp agent is a JVM option (`JVM_EXTRA_OPTS=-javaagent:/prometheus/jmx_prometheus_javaagent.jar=8090:...`), so it needs a restart to take effect — the operator does it one pod at a time the same safe way it handles a version upgrade, but it's still a real rolling restart. There's no new sidecar: pods stay `2/2`, the second container being `icarus`, so container counts tell you nothing about whether monitoring is on. Adding the Prober, Reaper and operator `ServiceMonitor`s restarts nothing. Never patch a cluster that already has a stress Job in flight; it'll drop the client's connections mid-run and invalidate the results. If a run is already going, wait for it to finish first, and never roll Prober, Cassandra and Reaper at the same time.

The `kubectl label` on `mr-cassop-metrics` is in-place and helm will revert it on the next `helm upgrade`. The durable fix belongs to `local-install`: add `release: prometheus-operator` alongside `operator: mr-cassop` under `monitoring.serviceMonitor.labels` in `local-values.yaml` (the chart's `toYaml` replaces the label map wholesale, so both keys have to be listed).

Verify — the only check that actually proves it's working is Prometheus's own target list. Four jobs should be `up`: the 3 Cassandra nodes on `:8090`, the prober on `:8888/metrics`, the reaper on `:8081/prometheusMetrics`, and `mr-cassop-metrics` on `:8329/metrics`. Give Prometheus 30-60s to reload after a `ServiceMonitor` appears; targets show `unknown` until the first scrape.

```bash
kubectl get --raw '/api/v1/namespaces/prometheus-operator/services/prometheus-operator-kube-p-prometheus:9090/proxy/api/v1/targets?state=active' \
  | python3 -c 'import sys,json; [print(t["labels"].get("job"), t["scrapeUrl"], t["health"], t.get("lastError","")) for t in json.load(sys.stdin)["data"]["activeTargets"] if "cassop" in json.dumps(t["labels"])]'
```

## 1. Custom profile: render and load it

Skip this step entirely when `PROFILE=default`.

Resolve `<PROFILE_FILE>`: `profiles/<PROFILE>.yaml` relative to this skill's directory if `PROFILE` has no `/`, otherwise treat `PROFILE` as a literal path.

Some profiles (like `device-data`) parameterize a recent time window via `${NOW_MS}` / `${SEVEN_DAYS_AGO_MS}` (and a day bucket via `${TODAY_EPOCH_DAY}` / `${SEVEN_DAYS_AGO_EPOCH_DAY}`) instead of hardcoding one — **cassandra-stress treats a `timestamp` column's population range as raw epoch-milliseconds, not an offset from "now"; omitting an explicit population range silently lands every row years in the past**, which then breaks any read query scoped to a recent window. Render with `envsubst` before loading:

```bash
export NOW_MS=$(date -u +%s000)
export SEVEN_DAYS_AGO_MS=$(date -u -d '-7 days' +%s000)
export TODAY_EPOCH_DAY=$(( $(date -u +%s) / 86400 ))
export SEVEN_DAYS_AGO_EPOCH_DAY=$(( $(date -u -d '-7 days' +%s) / 86400 ))
envsubst < <PROFILE_FILE> > /tmp/cassandra-stress-profile.yaml

kubectl create configmap cassandra-stress-profile -n <NAMESPACE> \
  --from-file=profile.yaml=/tmp/cassandra-stress-profile.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
```

Profiles create their keyspace and table with `IF NOT EXISTS`, so a changed `table_definition` does **not** apply to a table left over from an earlier run — the old schema stays and queries against new columns fail. After changing a profile's schema (including this `device-data` revision, which added the `day` bucket), drop the old keyspace first: `DROP KEYSPACE IF EXISTS device_stress;`.

If you add a new profile that needs its own time window or similar computed value, export it the same way before `envsubst` and note it in the profile's own header comment — don't hardcode absolute timestamps.

## 2. Build the cassandra-stress command

```bash
NODE=<CR_NAME>-cassandra-<DC>
COMMON="-mode native cql3 user=\$CASSANDRA_USER password=\$CASSANDRA_PASSWORD -node $NODE"

RATE="threads=<THREADS>"
[ -n "<TARGET_RATE>" ] && RATE="$RATE fixed=<TARGET_RATE>/s"

COUNT="n=<N>"            # or: COUNT="duration=<DURATION>"

STRESS_BIN="/opt/cassandra/tools/bin/cassandra-stress"   # not on PATH in the cassandra image

if [ "<PROFILE>" = "default" ]; then
  STRESS_CMD="$STRESS_BIN <OPERATION> $COUNT cl=<CL> -rate $RATE -schema 'replication(strategy=NetworkTopologyStrategy,<DC>=<RF>)' $COMMON"
else
  STRESS_CMD="$STRESS_BIN user profile=/profiles/profile.yaml 'ops(<OPERATION>=1)' $COUNT cl=<CL> -rate $RATE $COMMON"
fi
```

`\$CASSANDRA_USER` / `\$CASSANDRA_PASSWORD` are escaped on purpose — they must stay literal here and only get expanded later, inside the container, by the Job's own shell (see step 3), using the env vars sourced from `admin-secret`. `'ops(...)'` and `'replication(...)'` are single-quoted for a similar reason: `$STRESS_CMD` is run by `sh -c` inside the Job, where an unquoted `(` is a shell metacharacter (subshell grouping) and fails with `Syntax error: "(" unexpected`. Use single quotes, not backslashes — step 3 embeds the command in YAML, and a double-quoted YAML string rejects `\(` as an unknown escape.

Without `-schema`, cassandra-stress creates `keyspace1` with `SimpleStrategy` RF=1: every row lives on one node, so a single pod restart makes part of the data unavailable and the numbers don't reflect a replicated cluster. `-schema` only takes effect when stress creates the keyspace — if `keyspace1` already exists from an earlier run with different replication, drop it first (`DROP KEYSPACE keyspace1;`).

The built-in `keyspace1.standard1` write needs `-pop seq=1..<N>` (and a matching `-pop seq=<same range>` on any concurrent read) rather than a bare `n=`, for a "populate then read" flow — it errors on read ("Failed to execute warmup") against not-yet-written keys. A custom profile's named queries don't have this problem: querying a not-yet-inserted key just returns 0 rows, which is harmless, so write and read can run concurrently. If populating the default schema first, run write to completion before starting read.

## 3. Run it as a Job

```bash
JOB_NAME=cassandra-stress-<OPERATION>-$(date +%s)
```

For `PROFILE=default` (no profile volume needed):

```bash
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: $JOB_NAME
  namespace: <NAMESPACE>
  labels:
    app: cassandra-stress
spec:
  backoffLimit: 1
  activeDeadlineSeconds: 7200
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: cassandra-stress
          image: ghcr.io/cin/mr-cassop/cassandra:<IMAGE_TAG>
          imagePullPolicy: Never
          command: ["/bin/sh", "-c"]
          args:
            - |
              $STRESS_CMD
          env:
            - name: CASSANDRA_USER
              valueFrom: { secretKeyRef: { name: admin-secret, key: admin-role } }
            - name: CASSANDRA_PASSWORD
              valueFrom: { secretKeyRef: { name: admin-secret, key: admin-password } }
          resources:
            requests: { cpu: 250m, memory: 512Mi }
            limits: { cpu: 500m, memory: 1Gi }
EOF
```

For any custom profile, add the `profile.yaml` ConfigMap mount:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: $JOB_NAME
  namespace: <NAMESPACE>
  labels:
    app: cassandra-stress
spec:
  backoffLimit: 1
  activeDeadlineSeconds: 7200
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: cassandra-stress
          image: ghcr.io/cin/mr-cassop/cassandra:<IMAGE_TAG>
          imagePullPolicy: Never
          command: ["/bin/sh", "-c"]
          args:
            - |
              $STRESS_CMD
          env:
            - name: CASSANDRA_USER
              valueFrom: { secretKeyRef: { name: admin-secret, key: admin-role } }
            - name: CASSANDRA_PASSWORD
              valueFrom: { secretKeyRef: { name: admin-secret, key: admin-password } }
          resources:
            requests: { cpu: 250m, memory: 512Mi }
            limits: { cpu: 500m, memory: 1Gi }
          volumeMounts:
            - name: profile
              mountPath: /profiles
      volumes:
        - name: profile
          configMap:
            name: cassandra-stress-profile
EOF
```

`activeDeadlineSeconds: 7200` (2h) is a safety cap, not a target — pick `N`/`DURATION` and `TARGET_RATE` so the run finishes comfortably inside it. `args` uses a YAML block scalar (`|`) so the command is taken literally — no YAML escaping rules apply to it. Job names include a timestamp because `Job` specs are immutable: a rerun always needs a fresh name, never a re-`apply` over the old one.

## 4. Monitor and clean up

```bash
kubectl get job -n <NAMESPACE> $JOB_NAME -w
kubectl logs -n <NAMESPACE> job/$JOB_NAME -f
```

Cleanup between runs:

```bash
kubectl delete job -n <NAMESPACE> -l app=cassandra-stress
kubectl delete configmap cassandra-stress-profile -n <NAMESPACE> --ignore-not-found
```

## Full teardown (stop the cluster and operator too)

The cleanup above only clears stress artifacts so you can run again against the same cluster. To stop everything after you're done testing — the `CassandraCluster`, the operator, and any in-flight stress Job — tear down in this order, not in parallel, since deleting the CR out from under a running stress Job just makes it error instead of exiting cleanly:

```bash
kubectl delete job -n <NAMESPACE> -l app=cassandra-stress --ignore-not-found
kubectl delete configmap cassandra-stress-profile -n <NAMESPACE> --ignore-not-found
kubectl delete cassandraclusters.db.ibm.com -n <NAMESPACE> <CR_NAME> --ignore-not-found
helm uninstall mr-cassop -n mr-cassop-system
```

This is destructive — it drops any stress run in progress and deletes the cluster's data (PVCs aren't removed by this either way, but there's nothing left to attach them to). Confirm with the user before tearing down a cluster with a load test still running; only skip that confirmation if they've explicitly said to stop it now regardless. Bringing it back up afterward is the `local-install` skill's job, not this one's.

## Adding a new data model / read-write pattern

Drop a new [cassandra-stress user profile YAML](https://cassandra.apache.org/doc/latest/cassandra/tools/cassandra_stress.html) under `profiles/<name>.yaml`: `keyspace`/`keyspace_definition`, `table`/`table_definition`, `columnspec`, an `insert:` section for writes, and a `queries:` section (one entry per named read pattern) for reads. Then run with `PROFILE=<name>` and `OPERATION=insert` or `OPERATION=<query name>`.

Keep `keyspace_definition`'s replication strategy in sync with the target cluster's actual DC name and replica count if you're not using the `test-cluster` default (`dc1`, RF 3) — it's static text baked into the profile, not derived from `<DC>`.

## Example: the IoT `device_data` profile (ships in `profiles/device-data.yaml`)

5000 simulated devices, one partition per `(device_id, day)`, one row per `timestamp`, ~7 days of history, UCS compaction and a 7-day TTL:

```bash
# populate ~2M rows, throttled so it doesn't saturate a kind cluster (~85 min)
PROFILE=device-data OPERATION=insert N=2000000 THREADS=8 TARGET_RATE=400

# concurrent range-query read load, fixed low rate, 90 min
PROFILE=device-data OPERATION=by_device_range DURATION=90m THREADS=4 TARGET_RATE=50
```
