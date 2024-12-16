PACKAGE=github.com/openshift/cluster-node-tuning-operator
PACKAGE_BIN=$(lastword $(subst /, ,$(PACKAGE)))
PACKAGE_MAIN=$(PACKAGE)/cmd/$(PACKAGE_BIN)

# By default we build the same architecture we are running
# Override this by specifying a different GOARCH in your environment
HOST_ARCH ?= $(shell uname -m)

# Convert from uname format to GOARCH format
ifeq ($(HOST_ARCH),aarch64)
	HOST_ARCH=arm64
endif
ifeq ($(HOST_ARCH),x86_64)
	HOST_ARCH=amd64
endif

# Define GOARCH as HOST_ARCH if not otherwise defined
ifndef GOARCH
	GOARCH=$(HOST_ARCH)
endif

# Build-specific variables
OUT_DIR=_output
GOBINDATA_BIN=$(OUT_DIR)/go-bindata
BINDATA=pkg/manifests/bindata.go
ASSETS=$(shell find assets -name \*.yaml)
GO=GOARCH=$(GOARCH) GO111MODULE=on GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=$(GO) build -o $(OUT_DIR)/$(PACKAGE_BIN) -ldflags '-X $(PACKAGE)/version.Version=$(REV)' $(PACKAGE_MAIN)
GOFMT_CHECK=$(shell find . -not \( \( -wholename './.*' -o -wholename '*/vendor/*' \) -prune \) -name '*.go' | sort -u | xargs gofmt -s -l)
REV=$(shell git describe --long --tags --match='v*' --always --dirty)

# Upstream tuned daemon variables
TUNED_COMMIT:=HEAD

# API-related variables
API_TYPES_DIR:=pkg/apis
API_TYPES:=$(shell find $(API_TYPES_DIR) -name \*_types.go)
API_ZZ_GENERATED:=zz_generated.deepcopy
API_GO_HEADER_FILE:=$(API_TYPES_DIR)/header.go.txt

# Container image-related variables
IMAGE_BUILD_CMD?=podman build --no-cache --arch=$(GOARCH) --build-arg GOARCH=$(GOARCH)
IMAGE_PUSH_CMD=podman push
DOCKERFILE?=Dockerfile
REGISTRY?=quay.io
ORG?=openshift
TAG?=$(shell git rev-parse --abbrev-ref HEAD)
IMAGE?=$(REGISTRY)/$(ORG)/origin-cluster-node-tuning-operator:$(TAG)
IMAGE_BUILD_EXTRA_OPTS?=

# PAO variables
CLUSTER ?= "ci"
PAO_CRD_APIS :=$(addprefix ./$(API_TYPES_DIR)/performanceprofile/,v2 v1 v1alpha1)

PAO_E2E_SUITES := $(shell hack/list-test-bin.sh)

# golangci-lint variables
GOLANGCI_LINT_VERSION=1.62.2
GOLANGCI_LINT_BIN=$(OUT_DIR)/golangci-lint
GOLANGCI_LINT_VERSION_TAG=v${GOLANGCI_LINT_VERSION}

all: build

# Do not put any includes above the "all" target.  We want the default target to build
# the operator.
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
    targets/openshift/operator/profile-manifests.mk \
    targets/openshift/crd-schema-gen.mk \
)

# This target will be run in the Dockerfile to initialize the tuned submodule by cloning it.
# Moreover, this can be used to update the tuned repo to a specific commit.
update-tuned-submodule:
	(git submodule update --init --force && \
	  cd assets/tuned/tuned && \
	  git pull origin master && \
	  git checkout $(TUNED_COMMIT))

build: $(BINDATA) pkg/generated build-performance-profile-creator build-gather-sysinfo
	$(GO_BUILD_RECIPE)

$(BINDATA): $(GOBINDATA_BIN) $(ASSETS)
	$(GOBINDATA_BIN) -mode 420 -modtime 1 -pkg manifests -o $(BINDATA) assets/...
	gofmt -s -w $(BINDATA)

pkg/generated: $(API_TYPES)
	hack/update-codegen.sh
	touch $@

$(GOBINDATA_BIN):
	$(GO) build -o $(GOBINDATA_BIN) ./vendor/github.com/kevinburke/go-bindata/go-bindata

test-e2e: $(BINDATA)
	for d in core basic reboots reboots/sno deferred; do \
	  KUBERNETES_CONFIG="$(KUBECONFIG)" $(GO) test -v -timeout 40m ./test/e2e/$$d -ginkgo.v -ginkgo.no-color -ginkgo.fail-fast -ginkgo.label-filter=!flaky || exit; \
	done

.PHONY: test-e2e-local
test-e2e-local: $(BINDATA) performance-profile-creator-tests gather-sysinfo-tests
	$(GO_BUILD_RECIPE)
	for d in performanceprofile/functests-render-command/1_render_command; do \
	  $(GO) test -v -timeout 40m ./test/e2e/$$d -ginkgo.v -ginkgo.no-color -ginkgo.fail-fast || exit; \
	done

# This target ensures /manifests directory is up-to-date.  It takes advantage of yaml patching functionality of
# github.com/openshift/build-machinery-go
update-manifests: ensure-yq ensure-yaml-patch update-profile-manifests
	hack/update-crds.sh
	$(foreach p,$(wildcard manifests/*.crd.yaml-patch),$(call patch-crd-yaml-patch,$(basename $(p)).yaml,$(p)))

verify:	verify-gofmt

verify-gofmt:
ifeq (, $(GOFMT_CHECK))
	@echo "verify-gofmt: OK"
else
	@echo "verify-gofmt: ERROR: gofmt failed on the following files:"
	@echo "$(GOFMT_CHECK)"
	@echo ""
	@echo "For details, run: gofmt -d -s $(GOFMT_CHECK)"
	@echo ""
	@exit 1
endif

GOLANGCI_LINT := $(shell command -v ${GOLANGCI_LINT_BIN} 2> /dev/null)
GOLANGCI_LINT_LOCAL_VERSION := $(shell command ${GOLANGCI_LINT_BIN} --version 2> /dev/null | awk '{print $$4}')
golangci-lint: $(BINDATA)
ifdef GOLANGCI_LINT
	@echo "Found golangci-lint, version: $(GOLANGCI_LINT_LOCAL_VERSION)"
ifneq ($(GOLANGCI_LINT_LOCAL_VERSION),$(GOLANGCI_LINT_VERSION))
		@echo "Mismatch version,local: $(GOLANGCI_LINT_LOCAL_VERSION), expected: $(GOLANGCI_LINT_VERSION). Installing golangci-lint $(GOLANGCI_LINT_VERSION)"
		curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh| sh -s -- -b $(OUT_DIR) $(GOLANGCI_LINT_VERSION_TAG)
endif
else
	@echo "Installing golangci-lint"
	curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh| sh -s -- -b $(OUT_DIR) $(GOLANGCI_LINT_VERSION_TAG)
	$(GOLANGCI_LINT_BIN) --version
endif
	$(GOLANGCI_LINT_BIN) run --verbose --print-resources-usage -c .golangci.yaml

vet: $(BINDATA)
	$(GO) vet ./...

test-unit: $(BINDATA) test-fuzz
	$(GO) test ./cmd/... ./pkg/... -coverprofile cover.out

test-fuzz:
	$(GO) test ./pkg/apis/performanceprofile/v2 -fuzz=FuzzValidateCPUs -fuzztime=10s 

clean:
	$(GO) clean $(PACKAGE_MAIN)
	rm -rf $(BINDATA) $(OUT_DIR) tmp

local-image:
	$(IMAGE_BUILD_CMD) $(IMAGE_BUILD_EXTRA_OPTS) -t $(IMAGE) -f $(DOCKERFILE) .

local-image-push:
	$(IMAGE_PUSH_CMD) $(IMAGE_PUSH_EXTRA_OPTS) $(IMAGE)

# This will include additional actions on the update and verify targets to ensure that profile patches are applied.
# To update the manifests, run "make update-profile-manifests".
# to manifest files
# $0 - macro name
# $1 - target name
# $2 - profile patches directory
# $3 - manifests directory
$(call add-profile-manifests,manifests,./profile-patches,./manifests)

.PHONY: all build deepcopy crd-schema-gen test-e2e update-manifests verify verify-gofmt clean local-image local-image-push verify-codegen-crds

# PAO

.PHONY: generate-docs
generate-docs: dist-docs-generator
	hack/docs-generate.sh

.PHONY: dist-docs-generator
dist-docs-generator:
	@if [ ! -x $(OUT_DIR)/docs-generator ]; then\
		echo "Building docs-generator tool";\
		$(GO) build -ldflags="-s -w" -mod=vendor -o $(OUT_DIR)/docs-generator ./tools/docs-generator;\
	else \
		echo "Using pre-built docs-generator tool";\
	fi

.PHONY: dist-latency-tests
dist-latency-tests:
	./hack/build-latency-test-bin.sh

.PHONY: cluster-label-worker-cnf
cluster-label-worker-cnf:
	@echo "Adding worker-cnf label to worker nodes"
	hack/label-worker-cnf.sh

.PHONY: cluster-wait-for-pao-mcp
cluster-wait-for-pao-mcp:
    # NOTE: for CI this is done in the config suite of the functests!
    # Use this when deploying manifests manually with CLUSTER=manual
	@echo "Waiting for MCP to be updated"
	CLUSTER=$(CLUSTER) hack/wait-for-mcp.sh

.PHONY: cluster-deploy-pao
cluster-deploy-pao:
	@echo "Deploying PAO artifacts"
	CLUSTER=$(CLUSTER) hack/deploy.sh

.PHONY: pao-functests
pao-functests: cluster-label-worker-cnf pao-functests-only

.PHONY: pao-functests-only
pao-functests-only: $(BINDATA)
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-test.sh -t "test/e2e/performanceprofile/functests/0_config test/e2e/performanceprofile/functests/1_performance test/e2e/performanceprofile/functests/6_mustgather_testing test/e2e/performanceprofile/functests/10_performance_ppc" -p "-v -r --fail-fast  --flake-attempts=2 --junit-report=report.xml" -m "Running Functional Tests"

.PHONY: pao-functests-updating-profile
pao-functests-updating-profile: cluster-label-worker-cnf pao-functests-update-only

.PHONY: pao-functests-update-only
pao-functests-update-only: $(BINDATA)
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-test.sh -t "test/e2e/performanceprofile/functests/0_config test/e2e/performanceprofile/functests/2_performance_update test/e2e/performanceprofile/functests/3_performance_status test/e2e/performanceprofile/functests/7_performance_kubelet_node test/e2e/performanceprofile/functests/9_reboot" -p "-v -r --fail-fast --flake-attempts=2 --timeout=5h --junit-report=report.xml" -m "Running Functional Tests"

.PHONY: pao-functests-update-only-hypershift
pao-functests-update-only-hypershift: $(BINDATA)
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-test.sh -t "test/e2e/performanceprofile/functests/0_config ./test/e2e/performanceprofile/functests/2_performance_update ./test/e2e/performanceprofile/functests/7_performance_kubelet_node ./test/e2e/performanceprofile/functests/8_performance_workloadhints ./test/e2e/performanceprofile/functests/12_hypershift" -p "-v -r --label-filter=!(openshift||slow) --fail-fast --flake-attempts=2 --timeout=5h --junit-report=report.xml" -m "Running Functional Tests"

.PHONY: pao-functests-performance-workloadhints
pao-functests-performance-workloadhints: cluster-label-worker-cnf pao-functests-performance-workloadhints-only

.PHONY: pao-functests-performance-workloadhints-only
pao-functests-performance-workloadhints-only: $(BINDATA)
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-test.sh -t "test/e2e/performanceprofile/functests/0_config test/e2e/performanceprofile/functests/8_performance_workloadhints" -p "-v -r --fail-fast --flake-attempts=2 --timeout=5h --junit-report=report.xml" -m "Running Functional WorkloadHints Tests"

.PHONY: pao-functests-latency-testing
pao-functests-latency-testing: dist-latency-tests $(BINDATA)
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-test.sh -t "./test/e2e/performanceprofile/functests/0_config ./test/e2e/performanceprofile/functests/5_latency_testing" -p "-v -r --fail-fast --flake-attempts=2 --timeout=5h --junit-report=report.xml" -m "Running Functionalconfiguration latency Tests"

.PHONY: pao-functests-mixedcpus
pao-functests-mixedcpus: $(BINDATA)
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-test.sh -t "./test/e2e/performanceprofile/functests/0_config ./test/e2e/performanceprofile/functests/11_mixedcpus" -p "-v -r --fail-fast --flake-attempts=2 --junit-report=report.xml" -m "Running MixedCPUs Tests"

.PHONY: pao-functests-hypershift
pao-functests-hypershift: $(BINDATA)
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-test.sh -t "./test/e2e/performanceprofile/functests/0_config ./test/e2e/performanceprofile/functests/1_performance ./test/e2e/performanceprofile/functests/3_performance_status ./test/e2e/performanceprofile/functests/6_mustgather_testing" -p "-vv -r --label-filter="!openshift" --fail-fast --flake-attempts=2 --timeout=4h --junit-report=report.xml" -m "Running Functional Tests over Hypershift"

.PHONY: cluster-clean-pao
cluster-clean-pao:
	@echo "Cleaning up performance addons artifacts"
	hack/clean-deploy.sh

# Performance Profile Creator (PPC)
.PHONY: build-performance-profile-creator
build-performance-profile-creator:
	@echo "Building Performance Profile Creator (PPC)"
	LDFLAGS="-s -w -X ${PACKAGE}/cmd/performance-profile-creator/version.Version=${REV} "; \
	$(GO) build  -v $(LDFLAGS) -o $(OUT_DIR)/performance-profile-creator ./cmd/performance-profile-creator

.PHONY: performance-profile-creator-tests
performance-profile-creator-tests: build-performance-profile-creator
	@echo "Running Performance Profile Creator Tests"
	hack/run-test.sh -t "test/e2e/performanceprofile/functests-performance-profile-creator" -p "--v -r --fail-fast --flake-attempts=2" -m "Running Functional Tests" -r "--junit-report=/tmp/artifacts"

# Gather sysinfo binary for use in must-gather
.PHONY: build-gather-sysinfo
build-gather-sysinfo:
	@echo "Building gather-sysinfo"
	LDFLAGS="-s -w -X ${PACKAGE}/cmd/gather-sysinfo/version.Version=${REV} "; \
	$(GO) build -v $(LDFLAGS) -o $(OUT_DIR)/gather-sysinfo ./cmd/gather-sysinfo

.PHONY: gather-sysinfo-tests
gather-sysinfo-tests: build-gather-sysinfo
	@echo "Running gather-sysinfo Tests"
	$(GO) test -v ./cmd/gather-sysinfo


.PHONY: render-sync
render-sync: build
	hack/render-sync.sh

build-e2e-%:
	@hack/build-test-bin.sh $(shell echo $@ | sed -e 's/^build-e2e-//' )

pao-build-e2e-%:
	@hack/build-pao-test-bin.sh $(shell echo $@ | sed -e 's/^pao-build-e2e-//' )

.PHONY: pao-build-e2e
pao-build-e2e:
	@for suite in $(PAO_E2E_SUITES); do \
		hack/build-pao-test-bin.sh $$suite; \
	done

.PHONY: pao-clean-e2e
pao-clean-e2e:
	@rm -f _output/e2e-pao*.test
