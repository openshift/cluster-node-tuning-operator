#!/bin/bash

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
