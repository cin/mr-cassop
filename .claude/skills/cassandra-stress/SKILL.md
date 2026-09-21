---
name: cassandra-stress
description: Run cassandra-stress load-test Jobs (write and/or read) against an already-installed, ready CassandraCluster in the local "cassandra" kind cluster, using pluggable stress-profile YAMLs for different data models and read/write patterns. Ships with an IoT-style time-series "device_data" profile; add more under profiles/. Not for bringing a cluster up — see the local-install skill for that.
---

# mr-cassop cassandra-stress load testing

Runs `cassandra-stress` as a one-off Kubernetes `Job` against an already-running `CassandraCluster` (see the `local-install` skill to bring one up first). Supports the built-in `keyspace1.standard1` schema for a quick smoke load, and custom stress-profile YAMLs under `profiles/` for specific data models and read/write patterns.

## Inputs

- `CR_NAME` — CassandraCluster name (default `test-cluster`).
- `NAMESPACE` — (default `cassop`).
- `DC` — datacenter name, used to build the target service name (default `dc1`).
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
- `IMAGE_TAG` — cassandra image tag to run the stress client from (default `dev`). Must already be loaded into the kind cluster (`imagePullPolicy: Never`) — if not, run `kind load docker-image --name cassandra ghcr.io/cin/mr-cassop/cassandra:<IMAGE_TAG>` first (see the `local-install` skill).

Confirm `PROFILE`, `OPERATION`, and `N`/`DURATION` with the user before running if not already given.

## Pre-flight

```bash
kubectl get cassandraclusters.db.ibm.com -n <NAMESPACE> <CR_NAME> -o jsonpath='{.status.ready}'
kubectl get secret admin-secret -n <NAMESPACE> >/dev/null
```

Stop if the CR isn't `true`-ready or `admin-secret` doesn't exist — getting the cluster into that state is the `local-install` skill's job, not this one's.

## 1. Custom profile: render and load it

Skip this step entirely when `PROFILE=default`.

Resolve `<PROFILE_FILE>`: `profiles/<PROFILE>.yaml` relative to this skill's directory if `PROFILE` has no `/`, otherwise treat `PROFILE` as a literal path.

Some profiles (like `device-data`) parameterize a recent time window via `${NOW_MS}` / `${SEVEN_DAYS_AGO_MS}` instead of hardcoding one — **cassandra-stress treats a `timestamp` column's population range as raw epoch-milliseconds, not an offset from "now"; omitting an explicit population range silently lands every row years in the past**, which then breaks any read query scoped to a recent window. Render with `envsubst` before loading:

```bash
export NOW_MS=$(date -u +%s000)
export SEVEN_DAYS_AGO_MS=$(date -u -d '-7 days' +%s000)
envsubst < <PROFILE_FILE> > /tmp/cassandra-stress-profile.yaml

kubectl create configmap cassandra-stress-profile -n <NAMESPACE> \
  --from-file=profile.yaml=/tmp/cassandra-stress-profile.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
```

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
  STRESS_CMD="$STRESS_BIN <OPERATION> $COUNT -rate $RATE $COMMON"
else
  STRESS_CMD="$STRESS_BIN user profile=/profiles/profile.yaml ops\(<OPERATION>=1\) $COUNT -rate $RATE $COMMON"
fi
```

`\$CASSANDRA_USER` / `\$CASSANDRA_PASSWORD` are escaped on purpose — they must stay literal here and only get expanded later, inside the container, by the Job's own shell (see step 3), using the env vars sourced from `admin-secret`. `ops\(...\)` is escaped for the same reason: `$STRESS_CMD` ends up as a single string handed to `sh -c` inside the Job's `command`, and an unescaped `(` there is a shell metacharacter (subshell grouping), not a literal character — it'll fail with `Syntax error: "(" unexpected` otherwise.

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
          command: ["/bin/sh", "-c", "$STRESS_CMD"]
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
          command: ["/bin/sh", "-c", "$STRESS_CMD"]
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

`activeDeadlineSeconds: 7200` (2h) is a safety cap, not a target — pick `N`/`DURATION` and `TARGET_RATE` so the run finishes comfortably inside it. Job names include a timestamp because `Job` specs are immutable: a rerun always needs a fresh name, never a re-`apply` over the old one.

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

## Adding a new data model / read-write pattern

Drop a new [cassandra-stress user profile YAML](https://cassandra.apache.org/doc/latest/cassandra/tools/cassandra_stress.html) under `profiles/<name>.yaml`: `keyspace`/`keyspace_definition`, `table`/`table_definition`, `columnspec`, an `insert:` section for writes, and a `queries:` section (one entry per named read pattern) for reads. Then run with `PROFILE=<name>` and `OPERATION=insert` or `OPERATION=<query name>`.

Keep `keyspace_definition`'s replication strategy in sync with the target cluster's actual DC name and replica count if you're not using the `test-cluster` default (`dc1`, RF 3) — it's static text baked into the profile, not derived from `<DC>`.

## Example: the IoT `device_data` profile (ships in `profiles/device-data.yaml`)

5000 simulated devices, one row per `(device_id, timestamp)`, ~7 days of history per device:

```bash
# populate ~2M rows, throttled so it doesn't saturate a kind cluster (~85 min)
PROFILE=device-data OPERATION=insert N=2000000 THREADS=8 TARGET_RATE=400

# concurrent range-query read load, fixed low rate, 90 min
PROFILE=device-data OPERATION=by_device_range DURATION=90m THREADS=4 TARGET_RATE=50
```
