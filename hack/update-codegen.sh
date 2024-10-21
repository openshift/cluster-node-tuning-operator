#!/bin/bash

# Inspired by https://github.com/kubernetes/sample-controller/blob/master/hack/update-codegen.sh

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(realpath "${0%/*}/..")
pushd ${SCRIPT_ROOT}
CODEGEN_PKG=${CODEGEN_PKG:-$(ls -d -1 ${SCRIPT_ROOT}/vendor/k8s.io/code-generator 2>/dev/null)}

source "${CODEGEN_PKG}/kube_codegen.sh"

THIS_PKG=github.com/openshift/cluster-node-tuning-operator

gen_deepcopy() {
  echo "Generateing deepcopy code for API."
  kube::codegen::gen_helpers \
      --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
      "${SCRIPT_ROOT}/pkg/apis"
}

gen_client() {
  echo "Generating pkg/{clientset,informers,listers} for core NTO."
  kube::codegen::gen_client \
      --with-watch \
      --output-dir "${SCRIPT_ROOT}/pkg/generated" \
      --output-pkg "${THIS_PKG}/pkg/generated" \
      --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
      "${SCRIPT_ROOT}/pkg/apis"
}

gen_deepcopy
gen_client

popd
