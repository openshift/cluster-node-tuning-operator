PACKAGE=github.com/openshift/cluster-node-tuning-operator
MAIN_PACKAGE=$(PACKAGE)/cmd/cluster-node-tuning-operator

BIN=$(lastword $(subst /, ,$(MAIN_PACKAGE)))
BINDATA=pkg/manifests/bindata.go

GOFMT_CHECK=$(shell find . -not \( \( -wholename './.*' -o -wholename '*/vendor/*' -o -wholename './pkg/assets/bindata.go' -o -wholename './pkg/manifests/bindata.go' \) -prune \) -name '*.go' | sort -u | xargs gofmt -s -l)

DOCKERFILE=Dockerfile
IMAGE_TAG=openshift/origin-cluster-node-tuning-operator
IMAGE_REGISTRY=docker.io

vpath bin/go-bindata $(GOPATH)
GOBINDATA_BIN=bin/go-bindata

ENVVAR=GOOS=linux GOARCH=amd64 CGO_ENABLED=0
GOOS=linux
GO_BUILD_RECIPE=GOOS=$(GOOS) go build -o $(BIN) $(MAIN_PACKAGE)

all: generate build

build:
	$(GO_BUILD_RECIPE)

# Using "-modtime 1" to make generate target deterministic. It sets all file time stamps to unix timestamp 1
generate: $(GOBINDATA_BIN)
	go-bindata -mode 420 -modtime 1 -pkg manifests -o $(BINDATA) manifests/... assets/...

$(GOBINDATA_BIN):
	go get -u github.com/jteeuwen/go-bindata/...

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

clean:
	go clean
	rm -f $(BIN)

local-image:
ifdef USE_BUILDAH
	buildah bud -t $(IMAGE_TAG) -f $(DOCKERFILE) .
else
	docker build -t $(IMAGE_TAG) -f $(DOCKERFILE) .
endif

local-image-push:
	buildah push $(IMAGE_TAG) $(IMAGE_REGISTRY)/$(IMAGE_TAG)

.PHONY: all build generate verify verify-gofmt clean local-image local-image-push
