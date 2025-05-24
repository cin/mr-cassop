---
title: Development
slug: /development
---

### Requirements:

* Kubernetes 1.19 or newer. You can use [minikube](https://kubernetes.io/docs/setup/minikube/), [kind](https://github.com/kubernetes-sigs/kind), or [colima](https://github.com/abiosoft/colima) for local development.
* Go 1.24+ with enabled go modules
* [OperatorSDK](https://github.com/operator-framework/operator-sdk) v1.15.0+
* [kustomize](https://github.com/kubernetes-sigs/kustomize) 4.4.1+
* [helm](https://helm.sh/) v3.7.02+
* [docker](https://docs.docker.com/install/) with buildx support
* [goimports](https://godoc.org/golang.org/x/tools/cmd/goimports)
* [GolangCI-Lint](https://github.com/golangci/golangci-lint) 1.43.0+
* [kubebuilder](https://github.com/kubernetes-sigs/kubebuilder) to setup test environment
* [gomock](https://github.com/golang/mock)

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

# Full local development (operator + prober + cassandra)
./build-local-full.sh

# Build all 5 images
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

The build system is optimized for Apple Silicon with ARM64 by default, but supports multi-platform builds:

```bash
# Multi-platform builds for production
MULTI_PLATFORM=true ./build-images.sh
make docker-build-all-multiplatform
```

See [DOCKER_BUILD.md](../../DOCKER_BUILD.md) for comprehensive build documentation.

### Helm Deployment

Deploy the operator using the Helm chart:

```bash
# Build images first
./build-local-full.sh

# Create namespace
kubectl create namespace mr-cassop-system

# Deploy with custom values
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

Prepare configs and deploy C* cluster
```bash
cp config/samples/cassandracluster.yaml config/samples/cassandracluster_local.yaml
skaffold run
```

### Manual way

After you have your image with the code changes in the cluster, override the default container image through Helm values override. If you're pulling the image from a private container registry, you also need to specify the image pull secret. An another option would be to get the image to the cluster and set the `imagePullPolicy` to `Never`.

Your values override could look the following:

```
container:
  image: path.to.your.image:local
  imagePullSecret: icm-coreeng-pull-secret #if private registry is used
  imagePullPolicy: Always #Set to `Never` if the image exists only locally on the cluster
logLevel: debug
logFormat: json
```

Once the cluster is up and running, use the sample in `config/samples/cassandracluster.yaml` to run your cluster. The sample can be run without additional configuration, except you need to set your image pull secret name in the spec.

`kubectl apply -f config/samples/cassandracluster.yaml`

If all set correctly, you should see the components getting created.

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
