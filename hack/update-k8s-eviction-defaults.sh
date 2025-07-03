#!/bin/bash

# *********************************************************************************
# *** possibly update and run this script each time we update the k8s libraries ***
# *********************************************************************************

# this script pseudo-vendors kubernetes source code in order to let our
# code access the API defaults for eviction values.
# We currently use these values just and only in unit tests
# we cannot just vendor `k8s.io/kuberntes` because using that as library
# is strongly discouraged and not supported by k8s.
# Defaults are part of the API (TODO(fromani): citation needed) so they are very
# unlikely to change.
# Should the code of 8s be updated and the defaults reorganized (e.g.
# k8s moves to arch-independent defaults) this script needs to be updated

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(realpath "${0%/*}/..")
pushd ${SCRIPT_ROOT}

TAG=1.32.3
OS=$( go env GOOS )

KUBETAG=${KUBETAG:-$TAG}
KUBEOS=${KUBEOS:-$OS}

curl -L \
	-o ${SCRIPT_ROOT}/pkg/performanceprofile/k8simported/eviction/defaults_${KUBEOS}.go \
	https://raw.githubusercontent.com/kubernetes/kubernetes/refs/tags/v${KUBETAG}/pkg/kubelet/eviction/defaults_${KUBEOS}.go
