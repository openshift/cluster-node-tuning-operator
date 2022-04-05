PACKAGE=github.com/openshift/cluster-node-tuning-operator
PACKAGE_BIN=$(lastword $(subst /, ,$(PACKAGE)))
PACKAGE_MAIN=$(PACKAGE)/cmd/$(PACKAGE_BIN)

# Build-specific variables
OUT_DIR=_output
GOBINDATA_BIN=$(OUT_DIR)/go-bindata
BINDATA=pkg/manifests/bindata.go
ASSETS=$(shell find assets -name \*.yaml)
GO=GOOS=linux GO111MODULE=on GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=$(GO) build -o $(OUT_DIR)/$(PACKAGE_BIN) -ldflags '-X $(PACKAGE)/version.Version=$(REV)' $(PACKAGE_MAIN)
GOFMT_CHECK=$(shell find . -not \( \( -wholename './.*' -o -wholename '*/vendor/*' \) -prune \) -name '*.go' | sort -u | xargs gofmt -s -l)
REV=$(shell git describe --long --tags --match='v*' --always --dirty)

# Upstream tuned daemon variables
TUNED_REPO:=https://github.com/redhat-performance/tuned.git
TUNED_COMMIT:=682c47c0a9eb5596c2d396b6d0dae4e297414c50
TUNED_DIR:=daemon

# API-related variables
API_TYPES_DIR:=pkg/apis
API_TYPES:=$(shell find $(API_TYPES_DIR) -name \*_types.go)
API_ZZ_GENERATED:=zz_generated.deepcopy
API_GO_HEADER_FILE:=$(API_TYPES_DIR)/header.go.txt

# Container image-related variables
IMAGE_BUILD_CMD=podman build --no-cache
IMAGE_PUSH_CMD=podman push
DOCKERFILE=Dockerfile
REGISTRY=quay.io
ORG=openshift
TAG=$(shell git rev-parse --abbrev-ref HEAD)
IMAGE=$(REGISTRY)/$(ORG)/origin-cluster-node-tuning-operator:$(TAG)

# PAO variables
CLUSTER ?= "ci"
PAO_CRD_APIS :=$(addprefix ./$(API_TYPES_DIR)/performanceprofile/,v2 v1 v1alpha1)

all: build

# Do not put any includes above the "all" target.  We want the default target to build
# the operator.
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
    targets/openshift/operator/profile-manifests.mk \
    targets/openshift/crd-schema-gen.mk \
)

clone-tuned:
	(cd assets/tuned && \
	  rm -rf $(TUNED_DIR) && \
	  git clone -n $(TUNED_REPO) $(TUNED_DIR) && \
	  cd $(TUNED_DIR) && git checkout $(TUNED_COMMIT) && cd .. && \
	  rm -rf $(TUNED_DIR)/.git)

build: $(BINDATA) pkg/generated
	$(GO_BUILD_RECIPE)
	ln -sf $(PACKAGE_BIN) $(OUT_DIR)/openshift-tuned

$(BINDATA): $(GOBINDATA_BIN) $(ASSETS)
	$(GOBINDATA_BIN) -mode 420 -modtime 1 -pkg manifests -o $(BINDATA) assets/...
	gofmt -s -w $(BINDATA)

pkg/generated: $(API_TYPES)
	$(GO) run k8s.io/code-generator/cmd/deepcopy-gen \
	  --input-dirs $(PACKAGE)/$(API_TYPES_DIR)/tuned/v1,$(PACKAGE)/$(API_TYPES_DIR)/performanceprofile/v1alpha1,$(PACKAGE)/$(API_TYPES_DIR)/performanceprofile/v1,$(PACKAGE)/$(API_TYPES_DIR)/performanceprofile/v2 \
	  -O $(API_ZZ_GENERATED) \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --bounding-dirs $(PACKAGE)/$(API_TYPES_DIR) \
	  --output-base tmp
	$(GO) run k8s.io/code-generator/cmd/client-gen \
	  --clientset-name versioned \
	  --input-base '' \
	  --input $(PACKAGE)/$(API_TYPES_DIR)/tuned/v1 \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --output-package $(PACKAGE)/pkg/generated/clientset \
	  --output-base tmp
	$(GO) run k8s.io/code-generator/cmd/lister-gen \
	  --input-dirs $(PACKAGE)/$(API_TYPES_DIR)/tuned/v1 \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --output-package $(PACKAGE)/pkg/generated/listers \
	  --output-base tmp
	$(GO) run k8s.io/code-generator/cmd/informer-gen \
	  --input-dirs $(PACKAGE)/$(API_TYPES_DIR)/tuned/v1 \
	  --versioned-clientset-package $(PACKAGE)/pkg/generated/clientset/versioned \
	  --listers-package $(PACKAGE)/pkg/generated/listers \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --output-package $(PACKAGE)/pkg/generated/informers \
	  --output-base tmp
	tar c tmp | tar x --strip-components=4
	touch $@

$(GOBINDATA_BIN):
	$(GO) build -o $(GOBINDATA_BIN) ./vendor/github.com/kevinburke/go-bindata/go-bindata

test-e2e:
	for d in core basic reboots; do \
	  KUBERNETES_CONFIG="$(KUBECONFIG)" $(GO) test -v -timeout 40m ./test/e2e/$$d -ginkgo.v -ginkgo.noColor -ginkgo.failFast || exit; \
	done

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

vet: $(BINDATA)
	$(GO) vet ./...

test-unit: $(BINDATA)
	$(GO) test ./cmd/... ./pkg/... -coverprofile cover.out

clean:
	$(GO) clean $(PACKAGE_MAIN)
	rm -rf $(BINDATA) $(OUT_DIR)

local-image:
	$(IMAGE_BUILD_CMD) $(IMAGE_BUILD_EXTRA_OPTS) -t $(IMAGE) -f $(DOCKERFILE) .

local-image-push:
	$(IMAGE_PUSH_CMD) $(IMAGE_PUSH_EXTRA_OPTS) $(IMAGE)

# This will generate and patch the CRDs. To update the CRDs, run make update-codegen-crds.
# $1 - target name
# $2 - apis
# $3 - manifests
# $4 - output
$(call add-crd-gen,tuned,./$(API_TYPES_DIR)/tuned/v1,./manifests,./manifests)
$(call add-crd-gen,performanceprofile,$(PAO_CRD_APIS),./manifests,./manifests)

# This will include additional actions on the update and verify targets to ensure that profile patches are applied
# to manifest files
# $0 - macro name
# $1 - target name
# $2 - profile patches directory
# $3 - manifests directory
$(call add-profile-manifests,manifests,./profile-patches,./manifests)

.PHONY: all build deepcopy crd-schema-gen test-e2e verify verify-gofmt clean local-image local-image-push

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
pao-functests-only:
	@echo "Cluster Version"
	hack/show-cluster-version.sh
	hack/run-functests.sh

.PHONY: cluster-clean-pao
cluster-clean-pao:
	@echo "Cleaning up performance addons artifacts"
	hack/clean-deploy.sh
