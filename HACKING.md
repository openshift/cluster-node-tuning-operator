⚠⚠⚠ THIS IS A LIVING DOCUMENT AND LIKELY TO CHANGE QUICKLY ⚠⚠⚠

# Hacking on the Node Tuning Operator

These instructions have been tested on Fedora 36.

## Prerequisites

Most changes you'll want to test live against a real cluster. Instructions are TODO.

Go v1.18 is used for development of the NTO.

# Static Analysis

Static analysis (e.g. gofmt) can be executed with:

`make verify`

# Unit Tests

Unit tests (that don't interact with a running cluster) can be executed with:

`make test-unit`

# Local e2e tests

Local end-to-end tests (that don't interact with a running cluster) can be executed with:

`make test-e2e-local`

Note: This will install ginkgo if it is not already installed. You must ensure that the
go bin directory (usually $HOME/go/bin) is in your $PATH.

# Running in a cluster

During NTO development there are certain test scenarios
that require a cluster to properly test your code.

In those situations, you will need to:

1) build the NTO image you're interested in testing
2) TODO

## Build NTO image

To build an image that contains all NTO components run:

```
# Specify the registry
REGISTRY=quay.io
# Specify the registry user
ORG=username
# Specify the tag
TAG=tag

make local-image IMAGE=${REGISTRY}/${ORG}/cluster-node-tuning-operator:${TAG}
```

After the build is complete, you can push the image to a registry with:

```
# Specify the registry
REGISTRY=quay.io
# Specify the registry user
ORG=username
# Specify the tag
TAG=tag

make local-image-push IMAGE=${REGISTRY}/${ORG}/cluster-node-tuning-operator:${TAG}
```
