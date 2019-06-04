PACKAGE=github.com/openshift/cluster-node-tuning-operator
PACKAGE_BIN=$(lastword $(subst /, ,$(PACKAGE)))
PACKAGE_MAIN=$(PACKAGE)/cmd/manager

# Build-specific variables
GOBINDATA_BIN=./go-bindata
BINDATA=pkg/manifests/bindata.go
ENVVAR=GOOS=linux CGO_ENABLED=0
GOOS=linux
GO_BUILD_RECIPE=GOOS=$(GOOS) go build -o $(PACKAGE_BIN) -ldflags '-X $(PACKAGE)/version.Version=$(REV)' $(PACKAGE_MAIN)
GOFMT_CHECK=$(shell find . -not \( \( -wholename './.*' -o -wholename '*/vendor/*' \) -prune \) -name '*.go' | sort -u | xargs gofmt -s -l)
REV=$(shell git describe --long --tags --match='v*' --always --dirty)

# Container image-related variables
DOCKERFILE=Dockerfile
IMAGE_TAG=openshift/origin-cluster-node-tuning-operator
IMAGE_REGISTRY=quay.io

all: build

build: $(BINDATA)
	$(GO_BUILD_RECIPE)

# Using "-modtime 1" to make generate target deterministic. It sets all file time stamps to unix timestamp 1
generate $(BINDATA): $(GOBINDATA_BIN)
	$(GOBINDATA_BIN) -mode 420 -modtime 1 -pkg manifests -o $(BINDATA) assets/...
	gofmt -s -w $(BINDATA)

$(GOBINDATA_BIN):
	go build -o $(GOBINDATA_BIN) ./vendor/github.com/kevinburke/go-bindata/go-bindata

test-e2e: generate
	go test -v ./test/e2e/... -root $(PWD) -kubeconfig=$(KUBECONFIG) -tags e2e -globalMan manifests/02-crd.yaml

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

test:
	go test ./cmd/... ./pkg/... -coverprofile cover.out

clean:
	go clean
	rm -f $(PACKAGE_BIN) $(BINDATA) $(GOBINDATA_BIN)

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

.PHONY: all build generate test-e2e verify verify-gofmt clean local-image local-image-push
