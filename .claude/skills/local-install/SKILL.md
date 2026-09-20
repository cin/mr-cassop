---
name: local-install
description: Build, load and install mr-cassop into the local "cassandra" kind cluster (prometheus-operator stack + operator via Helm + a CassandraCluster CR), including dummy dev secrets and a PVC vs no-PVC choice. Use for repeatable local dev installs and in-place version upgrade testing. Not for production or real backup/restore credentials.
---

# mr-cassop local install

Repeatable install/upgrade flow for the `cassandra` kind cluster. Always confirm the target VERSION and PVC mode with the user before running if not already given in the request.

## Inputs

- `VERSION` — image tag to build/deploy, e.g. `0.7.2`. Required.
- `PERSISTENCE` — `pvc` or `no-pvc`. Required. Determines which cluster manifest to apply.
- `OVERWRITE` — optional, `true`/`false` (default `false`). When `true`, skip the pre-flight confirmation below and proceed straight through even if an install already exists. Pass it up front to avoid a mid-run prompt.
- Assumes the `cassandra` kind cluster already exists (`kind get clusters`).

## Pre-flight: check for an existing install

Run before step 0, every time — steps 0-7 are individually idempotent, but silently upgrading a cluster the caller didn't know was already running (e.g. mid-upgrade-test, or a leftover from a previous session) is a real risk, not just noise:

```bash
helm status mr-cassop -n mr-cassop-system 2>/dev/null
kubectl get cassandraclusters.db.ibm.com -n cassop test-cluster 2>/dev/null
kubectl get pods -n cassop -l cassandra-cluster-instance=test-cluster 2>/dev/null
```

If any of these return something (helm release exists, the CR exists, or C* pods are up) and `OVERWRITE` was not passed as `true`, stop and tell the user what's currently installed (helm release + revision, CR status, pod count/readiness) and ask whether to proceed — don't assume overwrite is wanted just because the command is idempotent. If `OVERWRITE=true` was given up front, skip the prompt and continue through the rest of the steps normally.

## 0. Prometheus operator stack

Idempotent — safe to run every time, no-ops if already installed at the same chart version:

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
kubectl create namespace prometheus-operator --dry-run=client -o yaml | kubectl apply -f -
helm upgrade prometheus-operator prometheus-community/kube-prometheus-stack -n prometheus-operator --install
```

mr-cassop's `monitoring.grafanaDashboard.datasourceName: "Prometheus"` in `local-values.yaml` assumes this release name/namespace — don't change either without updating that value too.

## 1. Get images for VERSION — pull released ones, only build what's missing

`.github/workflows/release.yml` publishes `ghcr.io/cin/mr-cassop/{operator,prober,cassandra,jolokia,icarus}:<VERSION>` for every GitHub release, tagged with the plain semver (matches `git tag`, e.g. `0.7.2`). If `<VERSION>` is a stable release, check GHCR before building anything — don't rebuild what's already published:

```bash
IMAGES=(operator prober cassandra jolokia icarus)
TO_BUILD=()

if [[ "<VERSION>" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  for img in "${IMAGES[@]}"; do
    if docker manifest inspect "ghcr.io/cin/mr-cassop/${img}:<VERSION>" >/dev/null 2>&1; then
      echo "found released ${img}:<VERSION> on GHCR — pulling"
      docker pull "ghcr.io/cin/mr-cassop/${img}:<VERSION>"
    else
      TO_BUILD+=("$img")
    fi
  done
else
  echo "<VERSION> isn't a stable semver tag (e.g. dev/local build) — building everything locally"
  TO_BUILD=("${IMAGES[@]}")
fi
```

Then build only what's left in `TO_BUILD`. `build-local.sh` always builds operator+prober(+cassandra) as one set, so if any of those three are missing, build all three together; jolokia/icarus build independently:

```bash
# if operator, prober, or cassandra is in TO_BUILD:
VERSION=<VERSION> ./build-local.sh --cassandra

# if jolokia is in TO_BUILD:
make docker-build-jolokia DOCKER_VERSION=<VERSION>

# if icarus is in TO_BUILD:
make docker-build-icarus DOCKER_VERSION=<VERSION>
```

(`build-local.sh` reads `VERSION`; the jolokia/icarus Makefile targets read `DOCKER_VERSION` instead — different variable, same effect. Don't mix them up.)

**Reaper is deliberately not part of this list.** `mr-cassop/values.yaml:24` pins it as `thelastpickle/cassandra-reaper:5.0.1`, an external Docker Hub image versioned independently of `<VERSION>` — comment there says so explicitly. Its pods don't use `imagePullPolicy: Never`, so it's pulled normally over the network and never needs building, a GHCR check, or `kind load`.

## 1b. Get the chart for VERSION — the released package for a stable version, the local tree otherwise

`mr-cassop/Chart.yaml` keeps `version`/`appVersion` hardcoded at `0.6.0` in the source tree on purpose (see its own comment) — the release workflow's `helm-chart` job packages `./mr-cassop` and overrides both from the git tag only when it builds the `mr-cassop-<VERSION>.tgz` asset attached to that GitHub release. There's no Helm chart repo index (the `gh-pages` branch is just the Docusaurus docs site) — the packaged `.tgz` release asset is the only place the actual released chart exists.

Installing against the chart directory in your local working tree instead is testing *your current tree's* chart, not necessarily the chart that version actually shipped with — those two usually match, but not always (uncommitted local edits, chart changes on `main` that haven't been released yet, etc). For a stable `<VERSION>`, use the real released package:

```bash
if [[ "<VERSION>" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  rm -rf "/tmp/mr-cassop-chart-<VERSION>" && mkdir -p "/tmp/mr-cassop-chart-<VERSION>"
  gh release download "<VERSION>" --repo cin/mr-cassop --pattern 'mr-cassop-*.tgz' \
    --dir "/tmp/mr-cassop-chart-<VERSION>" --clobber
  CHART_REF="/tmp/mr-cassop-chart-<VERSION>/mr-cassop-<VERSION>.tgz"
else
  echo "<VERSION> isn't a stable semver tag — using the local chart tree as-is"
  CHART_REF="mr-cassop"
fi
```

`$CHART_REF` is what step 5's `helm upgrade` installs from — carry it forward.

## 2. Load images into the kind cluster

`imagePullPolicy: Never` in the operator deployment — images must be loaded directly, never pulled.

```bash
kind load docker-image --name cassandra ghcr.io/cin/mr-cassop/operator:<VERSION>
kind load docker-image --name cassandra ghcr.io/cin/mr-cassop/prober:<VERSION>
kind load docker-image --name cassandra ghcr.io/cin/mr-cassop/cassandra:<VERSION>
kind load docker-image --name cassandra ghcr.io/cin/mr-cassop/jolokia:<VERSION>
kind load docker-image --name cassandra ghcr.io/cin/mr-cassop/icarus:<VERSION>
```

## 3. Namespaces (idempotent)

```bash
kubectl create namespace mr-cassop-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace cassop --dry-run=client -o yaml | kubectl apply -f -
```

## 4. Generate `local-values-<VERSION>.yaml` from the tracked template

`local-values.yaml` (tracked) is the template, pinned to `:dev` tags. Derive a version-pinned copy — don't hand-edit it each time:

```bash
sed 's/:dev/:<VERSION>/g' local-values.yaml > local-values-<VERSION>.yaml
```

This file is untracked/local by convention (see existing `local-values-0.7.2.yaml`) — don't `git add` it.

## 5. Install/upgrade the operator

```bash
helm upgrade mr-cassop "$CHART_REF" --install -n mr-cassop-system -f local-values-<VERSION>.yaml
```

`$CHART_REF` comes from step 1b — the downloaded `.tgz` for a stable version, or `mr-cassop` (the local tree) otherwise. Re-running this step with a new `VERSION` (after steps 1, 1b, 2 and 4 for that version) is the in-place upgrade path — the operator picks up new default images via env vars and rolls the existing `CassandraCluster`.

## 6. Dev secrets (dummy values — never real credentials)

`test-cluster.yaml` / `test-cluster-pvc.yaml` reference `imagePullSecretName: test-secret` and `adminRoleSecretName: admin-secret`, both in `cassop`. These are placeholders for local dev, not real registry/DB credentials:

```bash
kubectl create secret generic test-secret -n cassop \
  --type=kubernetes.io/dockerconfigjson --from-literal=.dockerconfigjson='{}' \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl get secret admin-secret -n cassop >/dev/null 2>&1 || \
  kubectl create secret generic admin-secret -n cassop \
    --from-literal=admin-role=admin \
    --from-literal=admin-password="$(openssl rand -base64 24)"
```

(Keys are exactly `admin-role` / `admin-password` — `api/v1alpha1/cassandracluster_types.go:40-41`.)

**Don't regenerate `admin-secret` on a rerun.** The operator diffs this secret's password against its own internally-tracked active admin password every reconcile (`controllers/admin_auth.go:50-76`) and, on a mismatch, does a live `ALTER ROLE` against the running Cassandra cluster. Re-applying a freshly-random password each run silently rotates live credentials instead of being a no-op — only create it if missing, as above.

## 7. Apply the CassandraCluster CR — PVC mode decides which file

- `no-pvc` → `kubectl apply -n cassop -f test-cluster.yaml` (`persistence.enabled: false`, pods are ephemeral — fine for a quick smoke test, but an in-place upgrade will lose all data on pod restart).
- `pvc` → `kubectl apply -n cassop -f test-cluster-pvc.yaml` (`persistence.enabled: true`, 5Gi `standard` storage class PVC per node — required for testing an in-place version upgrade actually preserves data across the rolling restart).

For the "upgrade in place" scenario the user is testing, always use the `pvc` manifest — data must survive the rolling image bump from step 5.

**Switching PERSISTENCE mode on an already-applied `test-cluster` is a no-op, not a migration.** StatefulSet `volumeClaimTemplates` are immutable in Kubernetes, so re-applying with the other manifest after the cluster already exists won't add or remove persistence. To actually change modes, delete the CR (and PVCs, if any) first — see Cleanup below — then apply the new manifest.

## Backup/restore credentials (documented, never run automatically)

Backup/restore to cloud storage (S3 tested and working per the 0.7.0 release; Azure/GCP untested) authenticates via a secret the operator/icarus sidecar reads, **not** created by this skill:

```bash
kubectl create secret generic backup-restore-credentials -n cassop \
  --from-literal=awsaccesskeyid="$(aws configure get aws_access_key_id --profile <profile>)" \
  --from-literal=awssecretaccesskey="$(aws configure get aws_secret_access_key --profile <profile>)" \
  --from-literal=awsregion="$(aws configure get region --profile <profile>)"
```

Only run this — with the user's own real AWS profile — when they explicitly want to exercise backup/restore. Never hardcode or fabricate key values, never run it as part of a routine install, and never put real key values in a committed file.

## Cleanup between runs

```bash
kubectl delete cassandraclusters.db.ibm.com -n cassop test-cluster
kubectl delete pvc -n cassop -l cassandra-cluster-dc=dc1   # only relevant in pvc mode
helm uninstall -n mr-cassop-system mr-cassop
```
