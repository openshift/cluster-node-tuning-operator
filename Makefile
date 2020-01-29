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

# API-related variables
API_TYPES_DIR:=pkg/apis/tuned/v1
API_TYPES:=$(wildcard $(API_TYPES_DIR)/types_*.go)
API_ZZ_GENERATED:=zz_generated.deepcopy
API_TYPES_GENERATED:=$(API_TYPES_DIR)/$(API_ZZ_GENERATED).go
API_GO_HEADER_FILE:=pkg/apis/header.go.txt

# Container image-related variables
DOCKERFILE=Dockerfile
IMAGE_TAG=openshift/origin-cluster-node-tuning-operator
IMAGE_REGISTRY=quay.io

all: build

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
	$(GO) run ./vendor/github.com/openshift/crd-schema-gen/cmd/crd-schema-gen/ --apis-dir pkg/apis --manifests-dir manifests/

$(GOBINDATA_BIN):
	$(GO) build -o $(GOBINDATA_BIN) ./vendor/github.com/kevinburke/go-bindata/go-bindata

test-e2e: $(BINDATA)
	KUBERNETES_CONFIG="$(KUBECONFIG)" $(GO) test -v -tags e2e ./test/e2e/... -run .

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
ifdef USE_BUILDAH
	buildah bud $(BUILDAH_OPTS) -t $(IMAGE_TAG) -f $(DOCKERFILE) .
else
	sudo docker build -t $(IMAGE_TAG) -f $(DOCKERFILE) .
endif

local-image-push:
ifdef USE_BUILDAH
	buildah push $(BUILDAH_OPTS) $(IMAGE_TAG) $(IMAGE_REGISTRY)/$(IMAGE_TAG)
else
	sudo docker tag $(IMAGE_TAG) $(IMAGE_REGISTRY)/$(IMAGE_TAG)
	sudo docker push $(IMAGE_REGISTRY)/$(IMAGE_TAG)
endif

.PHONY: all build deepcopy crd-schema-gen test-e2e verify verify-gofmt clean local-image local-image-push
