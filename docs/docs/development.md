---
title: Development
slug: /development
---

### Requirements:

* Kubernetes 1.28 or newer. You can use [minikube](https://kubernetes.io/docs/setup/minikube/), [kind](https://github.com/kubernetes-sigs/kind), or [colima](https://github.com/abiosoft/colima) for local development.
* Go 1.26+ with enabled go modules
* Node.js 24+ for building the documentation site
* [OperatorSDK](https://github.com/operator-framework/operator-sdk) v1.39.0+
* [kustomize](https://github.com/kubernetes-sigs/kustomize) 5.8.1+
* [helm](https://helm.sh/) v3.15.4+
* [docker](https://docs.docker.com/install/) with buildx support
* [goimports](https://godoc.org/golang.org/x/tools/cmd/goimports)
* [GolangCI-Lint](https://github.com/golangci/golangci-lint) 1.64.0+
* [setup-envtest](https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest) to install Kubernetes 1.32.x envtest assets
* [go.uber.org/mock](https://github.com/uber-go/mock)

## Run Operator 

There are two ways to run the operator:

### 1. In-Cluster Deployment (Recommended)

The operator can be deployed to Kubernetes using Helm charts. This is the recommended approach for both development and production.

### 2. Local Process (Development Only)

For rapid development iteration, the operator can also be run as a local process using `make run` with the `dev-env.sh` environment configuration.

To run your code with changes, you can either:
- Build Docker images and deploy via Helm (recommended)
- Run locally using `make run` (fastest iteration)
- Use [skaffold](https://skaffold.dev/) for automated builds

### Docker Build System

The project includes a comprehensive Docker build system optimized for development:

```bash
# Fast iteration - core images only (operator + prober)
./build-local.sh

# Essential local development images (operator + prober + cassandra)
./build-local.sh --cassandra

# Build all 5 images (operator, prober, cassandra, jolokia, icarus)
./build-images.sh

# Individual components
make docker-build-operator
make docker-build-prober
make docker-build-cassandra
make docker-build-jolokia
make docker-build-icarus

# Batch operations
make docker-build-core          # operator + prober
make docker-build-essential     # operator + prober + cassandra
make docker-build-monitoring    # jolokia + icarus
make docker-build-all          # all images
```

The build system defaults to the local Go platform (`go env GOOS/GOARCH`) and supports explicit multi-platform builds:

```bash
# Multi-platform builds for production
MULTI_PLATFORM=true ./build-images.sh
make docker-build-all-multiplatform
```

See [DOCKER_BUILD.md](https://github.com/cin/mr-cassop/blob/main/DOCKER_BUILD.md) for comprehensive build documentation.

### Helm Deployment

Deploy the operator using the Helm chart:

```bash
# Build images first
VERSION=dev ./build-images.sh

# Create namespace
kubectl create namespace mr-cassop-system

# Deploy with local image overrides
helm install mr-cassop ./mr-cassop -n mr-cassop-system -f local-values.yaml
```

### Local Process Development

For fastest iteration during development:

```bash
# Set up environment
source dev-env.sh

# Run operator locally
make run
```

### Skaffold way

Build and deploy the operator with skaffold:
```bash
skaffold run
```

Then create the required secrets and `CassandraCluster` resource from the [Quickstart](quickstart.md).

### Manual way

After you have your image with the code changes in the cluster, override the default container image through Helm values. If you're pulling the image from a private container registry, specify the image pull secret. Another option is to load the image directly into the cluster and set `imagePullPolicy` to `Never`.

Your values override could look the following:

```
container:
  image: path.to.your.image:local
  imagePullSecret: icm-coreeng-pull-secret #if private registry is used
  imagePullPolicy: Always #Set to `Never` if the image exists only locally on the cluster
logLevel: debug
logFormat: json
```

Once the operator is up, apply a `CassandraCluster` manifest. The [Quickstart](quickstart.md) contains a minimal 3-node example you can adapt for local development.

If all set correctly, you should see the components getting created.

## Repeatable Local Install / Upgrade Testing

The manual steps above (build, load, secrets, apply) are automated as a [Claude Code](https://claude.com/claude-code) skill at `.claude/skills/local-install/SKILL.md`, for repeatable installs against the `cassandra` kind cluster and for testing in-place version upgrades. If you're using Claude Code, invoke it with:

```
/local-install VERSION=0.7.2 PERSISTENCE=pvc
```

Pass `UI=false` to skip bringing up the [Nodetool UI](nodetool-ui.md) (on by default).

What it does, in order:

1. Installs the prometheus-operator stack, or upgrades it to the latest kube-prometheus-stack chart if it's already installed.
2. Gets images for `VERSION` — checks GHCR for already-published `ghcr.io/cin/mr-cassop/{operator,prober,cassandra,jolokia,icarus}:<version>` images first and only builds locally what isn't already released, instead of always rebuilding. Reaper is excluded on purpose: it's an external, independently-versioned image (`thelastpickle/cassandra-reaper:5.0.1`) that isn't loaded via `kind load`.
3. Gets the Helm chart for `VERSION` the same way: for a stable release, downloads the actual released `mr-cassop-<version>.tgz` package from that GitHub release (the only place the released chart exists — there's no chart repo index) rather than assuming the local working tree's chart matches what shipped. Falls back to the local chart tree for a dev/non-semver version. It then checks that chart's CRD for `spec.ui` to decide how to run the Nodetool UI, and gets the `ui` image if needed.
4. Loads the images into the kind cluster (`imagePullPolicy: Never`).
5. Creates namespaces, a version-pinned `local-values-<version>.yaml` (generated from the tracked `local-values.yaml` template), applies the chart's CRDs (Helm never upgrades them itself), and installs/upgrades the operator via Helm.
6. Creates dev-only secrets (`test-secret`, `admin-secret`) — created once and never regenerated on a rerun, since the operator treats a changed `admin-secret` password as a live credential-rotation request against the running cluster.
7. Applies a `CassandraCluster` CR — `test-cluster.yaml` (no PVCs, ephemeral) or `test-cluster-pvc.yaml` (5Gi PVC per node), depending on `PERSISTENCE`. Use `pvc` to test that an in-place upgrade preserves data across the rolling restart. When the chart supports it, `spec.ui.enabled: true` is set on the CR.
8. Makes the Nodetool UI available at `http://localhost:8090`: port-forwards the operator-managed UI service, or, for versions whose chart has no `spec.ui` (e.g. 0.7.x), runs `ui/` from source against the cluster's Prober.

It also runs a pre-flight check for an already-existing install (helm release, CR, or running pods) and asks before overwriting, unless `OVERWRITE=true` is passed up front. Real backup/restore credentials are documented (secret name and keys) but never created automatically — see the skill file for the exact command to run manually with your own cloud credentials.

## Tests

### Integration and unit tests

To run the tests, simply run `make tests` from the command line.

It will run the usual Go unit and integration tests, which utilize [testenv](https://book.kubebuilder.io/reference/envtest.html) to execute the test against on a semi-real k8s cluster. `testenv` requires assets that are installed with `kubebuilder`, so it must installed first.

Integration tests use the Ginkgo framework and are located in `./test/integration`. Ginkgo has its own [options]([Ginkgo flags](https://onsi.github.io/ginkgo/#the-ginkgo-cli)) that can be arguments to `go test`. For example, to run a specific test (`--focus` option):

`go test ./test/integration -ginkgo.focus="regex matcher"` 

For debugging you might want to see operator logs as the tests run. Pass the `-enableOperatorLogs=true` option to your  `go test ...` command to enable it:

`go test ./tests/integration -v -enableOperatorLogs=true`

or

`ginkgo -v -r ./tests/integration -- -enableOperatorLogs=true`

if you use Ginkgo CLI.

### E2E tests

E2E tests run on a k8s cluster. These tests deploy the C* Custom Resource Definition (CRD) in the k8s cluster.

>Note: before running, make sure the mr-cassop is deployed in your k8s namespace. 

To run e2e tests:

```
make e2e-tests
```

## Docs

To download and run docs locally, clone the repo and then go to the docs directory:

```console
cd ./mr-cassop/docs
```

If `npm` is not already installed it can be installed with:

```console
brew install npm
```

Then install the dependencies:

```console
npm install
```

To build and serve the documentation locally, run the command:

```console
npm start
```
