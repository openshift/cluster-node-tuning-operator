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
TUNED_COMMIT:=01fecec1d1a652489387a071c7cc7351b1638699
TUNED_DIR:=daemon

# API-related variables
API_TYPES_DIR:=pkg/apis/tuned/v1
API_TYPES:=$(wildcard $(API_TYPES_DIR)/*_types.go)
API_ZZ_GENERATED:=zz_generated.deepcopy
API_TYPES_GENERATED:=$(API_TYPES_DIR)/$(API_ZZ_GENERATED).go
API_GO_HEADER_FILE:=pkg/apis/header.go.txt

# Container image-related variables
IMAGE_BUILD_CMD=podman build --no-cache
IMAGE_PUSH_CMD=podman push
DOCKERFILE=Dockerfile
REGISTRY=quay.io
ORG=openshift
TAG=$(shell git rev-parse --abbrev-ref HEAD)
IMAGE=$(REGISTRY)/$(ORG)/origin-cluster-node-tuning-operator:$(TAG)

all: build

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
	  --input-dirs $(PACKAGE)/pkg/apis/tuned/v1 \
	  -O $(API_ZZ_GENERATED) \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --bounding-dirs $(PACKAGE)/pkg/apis \
	  --output-base tmp
	$(GO) run k8s.io/code-generator/cmd/client-gen \
	  --clientset-name versioned \
	  --input-base '' \
	  --input $(PACKAGE)/pkg/apis/tuned/v1 \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --output-package $(PACKAGE)/pkg/generated/clientset \
	  --output-base tmp
	$(GO) run k8s.io/code-generator/cmd/lister-gen \
	  --input-dirs $(PACKAGE)/pkg/apis/tuned/v1 \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --output-package $(PACKAGE)/pkg/generated/listers \
	  --output-base tmp
	$(GO) run k8s.io/code-generator/cmd/informer-gen \
	  --input-dirs $(PACKAGE)/pkg/apis/tuned/v1 \
	  --versioned-clientset-package $(PACKAGE)/pkg/generated/clientset/versioned \
	  --listers-package $(PACKAGE)/pkg/generated/listers \
	  --go-header-file $(API_GO_HEADER_FILE) \
	  --output-package $(PACKAGE)/pkg/generated/informers \
	  --output-base tmp
	tar c tmp | tar x --strip-components=4
	touch $@

crd-schema-gen:
	# TODO: look into using https://github.com/openshift/build-machinery-go/ and yaml patches
	$(GO) run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/ schemapatch:manifests=./manifests paths=./pkg/apis/tuned/v1 output:dir=./manifests
	yq w -i ./manifests/20-crd-tuned.yaml -s ./manifests/20-crd-tuned.yaml-patch

$(GOBINDATA_BIN):
	$(GO) build -o $(GOBINDATA_BIN) ./vendor/github.com/kevinburke/go-bindata/go-bindata

test-e2e: $(BINDATA)
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

test:
	$(GO) test ./cmd/... ./pkg/... -coverprofile cover.out

clean:
	$(GO) clean $(PACKAGE_MAIN)
	rm -rf $(BINDATA) $(OUT_DIR)

local-image:
	$(IMAGE_BUILD_CMD) $(IMAGE_BUILD_EXTRA_OPTS) -t $(IMAGE) -f $(DOCKERFILE) .

local-image-push:
	$(IMAGE_PUSH_CMD) $(IMAGE_PUSH_EXTRA_OPTS) $(IMAGE)

.PHONY: all build deepcopy crd-schema-gen test-e2e verify verify-gofmt clean local-image local-image-push
