-include vars.mk

OS = $(shell go env GOOS)
ARCH = $(shell go env GOARCH)
SHELL ?= bash
# Current Operator version
VERSION ?= 0.0.1
# Default bundle image tag
IMAGE_TAG_BASE ?= ghcr.io/cin/mr-cassop/operator
BUNDLE_IMG ?= $(IMAGE_TAG_BASE)-bundle:v$(VERSION)
# Options for 'bundle-build'
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

# Image URL to use for legacy build/push targets
IMG ?= $(REGISTRY)/operator:latest
# Docker registry and image configuration
REGISTRY ?= ghcr.io/cin/mr-cassop
DOCKER_VERSION ?= dev-$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
PLATFORM ?= $(OS)/$(ARCH)
MULTI_PLATFORM ?= false
BUILDX_BUILDER ?= mr-cassop-builder

# Individual image tags
OPERATOR_IMG ?= $(REGISTRY)/operator:$(DOCKER_VERSION)
PROBER_IMG ?= $(REGISTRY)/prober:$(DOCKER_VERSION)
CASSANDRA_IMG ?= $(REGISTRY)/cassandra:$(DOCKER_VERSION)
JOLOKIA_IMG ?= $(REGISTRY)/jolokia:$(DOCKER_VERSION)
ICARUS_IMG ?= $(REGISTRY)/icarus:$(DOCKER_VERSION)

# Produce Kubernetes v1 CRDs.
CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: manager

# Show help for Docker build targets
.PHONY: docker-help
docker-help:
	@echo "🚀 Docker Build Targets:"
	@echo ""
	@echo "  Individual Images ($(PLATFORM) by default):"
	@echo "    make docker-build-operator    # Build operator image"
	@echo "    make docker-build-prober      # Build prober image"
	@echo "    make docker-build-cassandra   # Build cassandra image"
	@echo "    make docker-build-jolokia     # Build jolokia image"
	@echo "    make docker-build-icarus      # Build icarus image"
	@echo ""
	@echo "  Batch Operations:"
	@echo "    make docker-build-core        # Build core images (operator + prober)"
	@echo "    make docker-build-essential   # Build essential images (+ cassandra)"
	@echo "    make docker-build-monitoring  # Build monitoring images (jolokia + icarus)"
	@echo "    make docker-build-all         # Build all images for $(PLATFORM)"
	@echo "    make docker-build-all-multiplatform  # Build for AMD64+ARM64 and push"
	@echo ""
	@echo "  Configuration:"
	@echo "    PLATFORM=linux/amd64         # Change target platform"
	@echo "    MULTI_PLATFORM=true          # Enable multi-platform build"
	@echo "    REGISTRY=myregistry           # Change registry"
	@echo "    DOCKER_VERSION=v1.0.0         # Change image version"
	@echo ""
	@echo "  Examples:"
	@echo "    make docker-build-operator PLATFORM=linux/amd64"
	@echo "    make docker-build-all MULTI_PLATFORM=true"

# Run unit tests
unit-tests:
	go test ./controllers/... -v -coverprofile=operator_unit.out -coverpkg=./...
	cd ./prober && go test ./... -v -coverprofile=prober_unit.out -coverpkg=./...

# Run integration tests
integration-tests:
	go test ./tests/integration -v -coverprofile=integration.out -coverpkg=./...

# Run e2e tests
e2e-tests:
	ginkgo -v --procs 20 --timeout=$(E2E_TIMEOUT) --always-emit-ginkgo-writer --progress --fail-fast ./tests/e2e/ -- \
		-test.v -test.timeout=$(E2E_TIMEOUT) \
		-operatorNamespace=$(K8S_NAMESPACE) \
		-imagePullSecret=$(IMAGE_PULL_SECRET) \
		-ingressDomain=$(INGRESS_DOMAIN) \
		-ingressSecret=$(INGRESS_SECRET) \
		-storageClassName=$(STORAGE_CLASS_NAME)

# Run all tests
all-tests: unit-tests integration-tests e2e-tests

# Run combined: unit and integration tests.
tests: generate fmt vet manifests
	go test ./controllers/... ./tests/integration -coverprofile=combined.out -v -coverpkg=./...


# Build manager binary
manager: generate fmt vet
	go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go

# Install CRDs into a cluster
install: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests kustomize
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role output:rbac:none paths="./api/..." paths="./controllers/..." output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role output:rbac:none paths="./api/..." paths="./controllers/..." output:crd:artifacts:config=$(ROOT_DIR)mr-cassop/crds
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=mr-cassop paths="./api/..." paths="./controllers/..." output:crd:none output:rbac:stdout > $(ROOT_DIR)mr-cassop/templates/clusterrole.yaml

# Run go fmt against code
fmt:
	go fmt ./api/... ./controllers/...
	go fmt ./main.go

# Run go vet against code
vet:
	go vet ./api/... ./controllers/...
	go vet ./main.go

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..." paths="./controllers/..."
	go run go.uber.org/mock/mockgen -package=mocks -source=./controllers/cql/cql.go -destination=./controllers/mocks/mock_cql.go
	go run go.uber.org/mock/mockgen -package=mocks -source=./controllers/prober/prober.go -destination=./controllers/mocks/mock_prober.go
	go run go.uber.org/mock/mockgen -package=mocks -source=./controllers/reaper/reaper.go -destination=./controllers/mocks/mock_reaper.go
	go run go.uber.org/mock/mockgen -package=mocks -source=./controllers/nodectl/nodectl.go -destination=./controllers/mocks/mock_nodectl.go
	go run go.uber.org/mock/mockgen -package=mocks -source=./controllers/icarus/icarus.go -destination=./controllers/mocks/mock_icarus.go

# Build the docker image (legacy target)
docker-build:
	docker build . -t ${IMG}

# Push the docker image (legacy target)
docker-push:
	docker push ${IMG}

# Setup buildx builder
.PHONY: docker-buildx-setup
docker-buildx-setup:
	@if ! docker buildx inspect $(BUILDX_BUILDER) > /dev/null 2>&1; then \
		echo "📦 Creating buildx builder '$(BUILDX_BUILDER)'..."; \
		docker buildx create --name $(BUILDX_BUILDER) --use --bootstrap; \
	else \
		echo "✅ Using existing buildx builder '$(BUILDX_BUILDER)'"; \
		docker buildx use $(BUILDX_BUILDER); \
	fi

# Build individual Docker images
.PHONY: docker-build-operator docker-build-prober docker-build-cassandra docker-build-jolokia docker-build-icarus

docker-build-operator: manager docker-buildx-setup
	@echo "🔨 Building operator image: $(OPERATOR_IMG)"
ifeq ($(MULTI_PLATFORM),true)
	@echo "   Building for multiple platforms: linux/amd64,linux/arm64"
	docker buildx build --platform=linux/amd64,linux/arm64 --build-arg VERSION=$(DOCKER_VERSION) -t $(OPERATOR_IMG) -t $(REGISTRY)/operator:latest --push .
else
	@echo "   Building for platform: $(PLATFORM)"
	docker buildx build --platform=$(PLATFORM) --build-arg VERSION=$(DOCKER_VERSION) -t $(OPERATOR_IMG) -t $(REGISTRY)/operator:latest --load .
endif
	@echo "✅ Built $(OPERATOR_IMG)"

docker-build-prober: docker-buildx-setup
	@echo "🔨 Building prober image: $(PROBER_IMG)"
ifeq ($(MULTI_PLATFORM),true)
	@echo "   Building for multiple platforms: linux/amd64,linux/arm64"
	docker buildx build --platform=linux/amd64,linux/arm64 --build-arg VERSION=$(DOCKER_VERSION) -f prober/Dockerfile -t $(PROBER_IMG) -t $(REGISTRY)/prober:latest --push prober
else
	@echo "   Building for platform: $(PLATFORM)"
	docker buildx build --platform=$(PLATFORM) --build-arg VERSION=$(DOCKER_VERSION) -f prober/Dockerfile -t $(PROBER_IMG) -t $(REGISTRY)/prober:latest --load prober
endif
	@echo "✅ Built $(PROBER_IMG)"

docker-build-cassandra: docker-buildx-setup
	@echo "🔨 Building cassandra image: $(CASSANDRA_IMG)"
ifeq ($(MULTI_PLATFORM),true)
	@echo "   Building for multiple platforms: linux/amd64,linux/arm64"
	docker buildx build --platform=linux/amd64,linux/arm64 -t $(CASSANDRA_IMG) -t $(REGISTRY)/cassandra:latest --push cassandra
else
	@echo "   Building for platform: $(PLATFORM)"
	docker buildx build --platform=$(PLATFORM) -t $(CASSANDRA_IMG) -t $(REGISTRY)/cassandra:latest --load cassandra
endif
	@echo "✅ Built $(CASSANDRA_IMG)"

docker-build-jolokia: docker-buildx-setup
	@echo "🔨 Building jolokia image: $(JOLOKIA_IMG)"
ifeq ($(MULTI_PLATFORM),true)
	@echo "   Building for multiple platforms: linux/amd64,linux/arm64"
	docker buildx build --platform=linux/amd64,linux/arm64 -t $(JOLOKIA_IMG) -t $(REGISTRY)/jolokia:latest --push jolokia
else
	@echo "   Building for platform: $(PLATFORM)"
	docker buildx build --platform=$(PLATFORM) -t $(JOLOKIA_IMG) -t $(REGISTRY)/jolokia:latest --load jolokia
endif
	@echo "✅ Built $(JOLOKIA_IMG)"

docker-build-icarus: docker-buildx-setup
	@echo "🔨 Building icarus image: $(ICARUS_IMG)"
ifeq ($(MULTI_PLATFORM),true)
	@echo "   Building for multiple platforms: linux/amd64,linux/arm64"
	docker buildx build --platform=linux/amd64,linux/arm64 -t $(ICARUS_IMG) -t $(REGISTRY)/icarus:latest --push icarus
else
	@echo "   Building for platform: $(PLATFORM)"
	docker buildx build --platform=$(PLATFORM) -t $(ICARUS_IMG) -t $(REGISTRY)/icarus:latest --load icarus
endif
	@echo "✅ Built $(ICARUS_IMG)"

# Build all images
docker-build-all: docker-build-operator docker-build-prober docker-build-cassandra docker-build-jolokia docker-build-icarus
	@echo "🎉 All images built successfully!"

# Build all images for multiple platforms and push
docker-build-all-multiplatform:
	$(MAKE) docker-build-all MULTI_PLATFORM=true
	@echo "🎉 All multi-platform images built and pushed successfully!"

# Build core images for local development (fast)
docker-build-core: docker-build-operator docker-build-prober
	@echo "🎉 Core images (operator + prober) built successfully!"

# Build essential images for full local development 
docker-build-essential: docker-build-operator docker-build-prober docker-build-cassandra
	@echo "🎉 Essential images (operator + prober + cassandra) built successfully!"

# Build monitoring images
docker-build-monitoring: docker-build-jolokia docker-build-icarus
	@echo "🎉 Monitoring images (jolokia + icarus) built successfully!"

# Push individual images
docker-push-operator:
	docker push $(OPERATOR_IMG)
	docker push $(REGISTRY)/operator:latest

docker-push-prober:
	docker push $(PROBER_IMG)  
	docker push $(REGISTRY)/prober:latest

docker-push-cassandra:
	docker push $(CASSANDRA_IMG)
	docker push $(REGISTRY)/cassandra:latest

docker-push-jolokia:
	docker push $(JOLOKIA_IMG)
	docker push $(REGISTRY)/jolokia:latest

docker-push-icarus:
	docker push $(ICARUS_IMG)
	docker push $(REGISTRY)/icarus:latest

# Push all images
docker-push-all: docker-push-operator docker-push-prober docker-push-cassandra docker-push-jolokia docker-push-icarus

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

kustomize:
ifeq (, $(shell which kustomize))
	@{ \
	set -e ;\
	KUSTOMIZE_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$KUSTOMIZE_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION) ;\
	rm -rf $$KUSTOMIZE_GEN_TMP_DIR ;\
	}
KUSTOMIZE=$(GOBIN)/kustomize
else
KUSTOMIZE=$(shell which kustomize)
endif

# Generate bundle manifests and metadata, then validate generated files.
bundle: manifests kustomize
	operator-sdk generate kustomize manifests -q
	kustomize build config/manifests | operator-sdk generate bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	operator-sdk bundle validate ./bundle

# Build the bundle image.
bundle-build:
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: opm
OPM = ./bin/opm
opm:
ifeq (,$(wildcard $(OPM)))
ifeq (,$(shell which opm 2>/dev/null))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPM)) ;\
	curl -sSLo $(OPM) https://github.com/operator-framework/operator-registry/releases/download/v1.15.1/$(OS)-$(ARCH)-opm ;\
	chmod +x $(OPM) ;\
	}
else
OPM = $(shell which opm)
endif
endif
BUNDLE_IMGS ?= $(BUNDLE_IMG)
CATALOG_IMG ?= $(IMAGE_TAG_BASE)-catalog:v$(VERSION) ifneq ($(origin CATALOG_BASE_IMG), undefined) FROM_INDEX_OPT := --from-index $(CATALOG_BASE_IMG) endif
.PHONY: catalog-build
catalog-build: opm
	$(OPM) index add --container-tool docker --mode semver --tag $(CATALOG_IMG) --bundles $(BUNDLE_IMGS) $(FROM_INDEX_OPT)

.PHONY: catalog-push
catalog-push: ## Push the catalog image.
	$(MAKE) docker-push IMG=$(CATALOG_IMG)
