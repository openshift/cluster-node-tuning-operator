#!/bin/bash

# expect oc to be in PATH by default
OC_TOOL="${OC_TOOL:-oc}"

function cluster_version_info() {
    local msg="${1}"
    local kubeconfig="${2}"

    echo "${msg}"
    if [[ -n "${kubeconfig}"  ]]; then
          kubeconfig_arg="--kubeconfig=${kubeconfig}"
    fi
    ${OC_TOOL} "${kubeconfig_arg}" version || :
    ${OC_TOOL} "${kubeconfig_arg}" get nodes -o custom-columns=VERSION:.status.nodeInfo.kubeletVersion || :
    ${OC_TOOL} "${kubeconfig_arg}" get clusterversion || :
}

if [[ ${CLUSTER_TYPE} == "hypershift" ]]; then
  # on u/s ci, the cli directory path is different
  CLI_DIR="${CLI_DIR:-/usr/bin}"
  OC_TOOL="${CLI_DIR}"/oc
  cluster_version_info "Management cluster version" "${HYPERSHIFT_MANAGEMENT_CLUSTER_KUBECONFIG}"
  cluster_version_info "Hosted cluster version" "${HYPERSHIFT_HOSTED_CLUSTER_KUBECONFIG}"
else
  cluster_version_info "Cluster version"
fi
