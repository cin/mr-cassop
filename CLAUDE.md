# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

mr-cassop is a Kubernetes operator (Go, controller-runtime/kubebuilder v3) that deploys and manages
multi-region Apache Cassandra clusters via a single `CassandraCluster` CRD (`api/v1alpha1`, group `db.ibm.com`).
It is a fork of `IBM/mr-cassop`, itself descended from the original IBM Cassandra operator, modernized to
Go 1.26 / Kubernetes 1.36 libraries / controller-runtime v0.24.1 / Cassandra 4.1.

## Commands

### Build / run
- `make manager` — generate, fmt, vet, then build the operator binary to `bin/manager`.
- `make run` — generate, fmt, vet, manifests, then `go run ./main.go` against the cluster in `~/.kube/config`.
  Source `dev-env.sh` first to populate the required env vars (`Config` in `controllers/config/config.go`
  requires `DEFAULT_CASSANDRA_IMAGE`, `DEFAULT_PROBER_IMAGE`, `DEFAULT_JOLOKIA_IMAGE`, `DEFAULT_REAPER_IMAGE`,
  `DEFAULT_ICARUS_IMAGE` — the process will not start without them).
- `make manifests` — regenerate CRDs and RBAC into `config/crd/bases`, `mr-cassop/crds`, and
  `mr-cassop/templates/clusterrole.yaml` via `controller-gen`. Run after changing `api/v1alpha1` types or
  `+kubebuilder:rbac` markers in `controllers/`.
- `make generate` — regenerate `zz_generated.deepcopy.go` and all mocks in `controllers/mocks` (mockgen,
  sourced from `cql/cql.go`, `prober/prober.go`, `reaper/reaper.go`, `nodectl/nodectl.go`, `icarus/icarus.go`).
  Run this after changing any of those five client interfaces.
- `./build-local.sh` / `./build-local.sh --cassandra` — fast local image builds (core, or core+cassandra) for
  the local Docker platform. `./build-images.sh` builds all five images. See `DOCKER_BUILD.md` for the full
  matrix (`make docker-build-operator|prober|cassandra|jolokia|icarus`, `docker-build-core|essential|monitoring|all`).
- One-line local demo: `./build-local.sh --cassandra && kubectl create namespace mr-cassop-system && helm install mr-cassop ./mr-cassop -n mr-cassop-system -f local-values.yaml`.

### Tests
- `make unit-tests` — `go test ./controllers/...` (root module) + `go test ./...` inside `prober/` (separate
  Go module, its own `go.mod`/`go.sum`).
- `make integration-tests` — `go test ./tests/integration`. Ginkgo-based, uses envtest (`setup-envtest`,
  Kubernetes 1.32.x assets) to run against a semi-real API server — no real Cassandra required, but envtest
  assets must be installed first (see CI job `integration-tests` in `.github/workflows/pull_request.yml` for
  the `setup-envtest use 1.32.x` invocation and `KUBEBUILDER_ASSETS` env var).
- `make tests` — generate, fmt, vet, manifests, then unit + integration tests combined with coverage.
- Run a single integration test: `go test ./tests/integration -ginkgo.focus="regex matcher"`.
- See operator logs during integration tests: `go test ./tests/integration -v -enableOperatorLogs=true`.
- `make e2e-tests` — full Ginkgo e2e suite (`tests/e2e/`) against a real cluster; requires mr-cassop already
  deployed. Needs `K8S_NAMESPACE`, `IMAGE_PULL_SECRET`, `INGRESS_DOMAIN`, `INGRESS_SECRET`,
  `STORAGE_CLASS_NAME` (see `vars.mk` / CI). Gated in PR CI behind the `run-e2e` label.
- `make fmt` / `make vet` — operator only (`./api/...`, `./controllers/...`, `main.go`). The `prober/` module
  is vetted/tested separately (`cd prober && go vet ./... && go test ./...`).
- Helm sanity: `helm lint ./mr-cassop` and `helm template mr-cassop ./mr-cassop --namespace mr-cassop-system`.
- Docs build check: `cd docs && npm ci && npm run build` (Node 24+).

### CI gate to know about
PR CI (`.github/workflows/pull_request.yml`) runs a Trivy CRITICAL-severity scan against every built image
(operator, prober, cassandra, jolokia, icarus) in the `image-builds` job, and it fails the job for **any** PR
regardless of what that PR actually touches — a vulnerable transitive Go dependency (e.g. `golang.org/x/crypto`)
will fail unrelated PRs (docs-only, GH Actions bumps, etc.) until it's patched on that branch.

## Architecture

### Two Go modules
- Root module (`github.com/cin/mr-cassop`) — the operator itself: `api/`, `controllers/`, `main.go`, `tests/`.
- `prober/` — a fully separate Go module (its own `go.mod`), built and deployed as its own image.

### Reconciliation shape
There is one reconciler, `CassandraClusterReconciler` (`controllers/controller.go`), and one CRD kind that
drives most of the system: `CassandraCluster`. (`CassandraBackup`/`CassandraRestore` have their own small
reconcilers in `controllers/cassandrabackup` and `controllers/cassandrarestore`, backed by Icarus.)
`Reconcile` -> `reconcileWithContext` runs a long, mostly-linear sequence of `reconcile<Thing>` steps (RBAC,
TLS secrets, maintenance configmap, Prometheus configmap, collectd configmap, service monitor, Prober
deployment, then per-DC Cassandra StatefulSets, keyspaces, roles, Reaper, repair schedules, etc.). Each step
lives in its own `controllers/cassandra_*.go` / `controllers/<thing>.go` file rather than being grouped by
package — when working on one concern (e.g. TLS, scaling, network policies), grep for the matching filename
in `controllers/` rather than expecting a subpackage.

Retryable/transient conditions (Prober not ready yet, TLS secret not found, region not ready, K8s resource
conflict) are threaded back as `ctrl.Result{RequeueAfter: r.Cfg.RetryDelay}` rather than errors — errors are
reserved for conditions that should show up as reconcile failures.

### External component clients are injected as factories, not constructed inline
`CassandraClusterReconciler` holds four factory functions (set once in `main.go`), each returning an
interface implemented against a mock in `controllers/mocks/` (regenerated via `make generate`):
- `ProberClient` -> `controllers/prober.ProberClient` — talks to the per-cluster Prober HTTP API.
- `CqlClient` -> `controllers/cql.CqlClient` — CQL access to the Cassandra cluster itself (gocql).
- `ReaperClient` -> `controllers/reaper.ReaperClient` — talks to the per-cluster Reaper (repairs) HTTP API.
- `NodectlClient` -> `controllers/nodectl.Nodectl` — JMX operations against a node via Jolokia.
- (Backup/restore reconcilers separately inject an `icarus.Icarus` factory the same way.)
This indirection is what makes the controller and integration-test suites mockable without a real cluster —
when adding a new external call, prefer extending one of these interfaces over reaching for a client directly
in reconciler code.

### The supporting components, and why they exist
- **Prober** (`controllers/prober/`, deployed via `controllers/prober.go`; source in `prober/`) — per-cluster
  sidecar service. Polls every Cassandra node's JMX endpoint state (`AllEndpointStates`/`SimpleStates`, parsed
  from the gossip MBean in `prober/jolokia/cassandra.go`) via Jolokia, and uses that cross-node view to answer
  pod readiness probes (a node is only "ready" once its peers' gossip state agrees — see
  `prober/prober/node_states.go:isNodeReady`). It also tracks and exposes region/DC/seed state used for
  cross-region coordination (`/region-ready`, `/dcs`, `/seeds`, `/region-ips`, `/reaper-ready`, `/reaper-ips`
  in `prober/prober/prober.go`). Effectively this is the project's existing "Cassandra sidecar" — it already
  aggregates gossip/nodetool-equivalent state across nodes, just not exposed as a general nodetool-status API.
- **Jolokia** — JMX-to-HTTP bridge; runs alongside Prober and is how both Prober and `nodectl` reach Cassandra's
  MBeans without a JMX client.
- **Reaper** — automated repair scheduling (`controllers/reaper.go`, `controllers/repair_schedules.go`).
- **Icarus** — backup/restore sidecar in each Cassandra pod, driven by the `CassandraBackup`/`CassandraRestore`
  reconcilers.

### Multi-region model
A single `CassandraCluster` spec can span multiple Kubernetes clusters/regions. One StatefulSet is created
per datacenter; Prober instances coordinate seed discovery and readiness across regions (see
`docs/docs/multi-region-cluster-configuration.md` and `docs/docs/architecture-overview.md` for the full
component diagram).

### Docs site
`docs/` is a separate Docusaurus (Node/npm) project, deployed to GitHub Pages (`gh-pages` branch) via
`.github/workflows/release.yml`. Content lives in `docs/docs/*.md` and is the canonical source for
per-component behavior (prober, reaper, jolokia, security, quickstart, etc.) beyond what's summarized here.
