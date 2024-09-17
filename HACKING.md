⚠⚠⚠ THIS IS A LIVING DOCUMENT AND LIKELY TO CHANGE QUICKLY ⚠⚠⚠

# Hacking on the Node Tuning Operator

These instructions have been tested on Fedora 39.

## Prerequisites

Most changes you'll want to test live against a real cluster. Instructions are TODO.

Go v1.21 is used for development of the NTO.

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

1) Build the NTO image you're interested in testing
2) Push the image to an OpenShift cluster

To build the repo's code as an image and push it to the cluster we have a script that 
can automate this for you: 

`ORG=quay-user hack/deploy-custom-nto.sh`

It is also possible to opt for a specific revision of [TuneD](https://github.com/redhat-performance/tuned) by specifying: `TUNED_COMMIT=commit-hash`

We recommend running some e2e tests to verify the custom image works as expected.

# Cross Compiling

By default the build will compile using the architecture the system is currently using.

You can specify a cross compiling architecture by setting `GOARCH` in your environment.

For example, to cross-compile for the aarch64 architecture, use the following:

```bash
GOARCH=arm64 make build
```

For QEMU user mode emulation, make sure you have the appropriate static binaries installed.  For aarch64 builds above
on Fedora, the package is qemu-user-static-aarch64.

To cross-compile for x86_64 architecture (e.g. from Apple's M hardware), use the following:

```bash
GOARCH=amd64 make build
```

# Local build failed with error 125: invalid reference format

By default the image tag is generated using the name of the local git branch. This behaviour will cause error 125 if you have a special character in your local git branch name. You should specify a tag manually instead:

```bash
TAG=my-custom-tag make local-image
```
