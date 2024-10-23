#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(realpath "${0%/*}/..")
pushd ${SCRIPT_ROOT}

# Consider generating the CRDs completely from scratch/API packages.  This would be cleaner, but
# also less convenient at additional cost of extra +kubebuilder tags in API packages and yaml-patch
# content such as .spec.versions[].additionalPrinterColumns for profiles.tuned.openshift.io.
gen_merge_crds() {
  echo "Generating OpenAPI v3 schemas for API packages and merging them into existing CRD manifests"

  GO111MODULE=on GOFLAGS=-mod=vendor go run sigs.k8s.io/controller-tools/cmd/controller-gen \
    schemapatch:manifests=./manifests output:dir=./manifests paths=./pkg/apis/...
}

gen_merge_crds

popd
