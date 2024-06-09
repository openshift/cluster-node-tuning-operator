#!/bin/bash

set -e

# expect oc to be in PATH by default
OC_TOOL="${OC_TOOL:-oc}"

# Label 1 worker node

function label_worker_cnf() {
    local kubeconfig="${1}"

    echo "[INFO]: Labeling 1 worker node with worker-cnf"
    if [[ -n "${kubeconfig}"  ]]; then
        kubeconfig_arg="--kubeconfig=${kubeconfig}"
    fi
    node=$(${OC_TOOL} ${kubeconfig_arg} get nodes --selector='node-role.kubernetes.io/worker,!node-role.kubernetes.io/master' -o name | head -1)
    echo ${OC_TOOL} label --overwrite $node node-role.kubernetes.io/worker-cnf=""
    ${OC_TOOL} label ${kubeconfig_arg} --overwrite $node node-role.kubernetes.io/worker-cnf=""
}

if [[ ${CLUSTER_TYPE} == "hypershift" ]]; then
    CLI_DIR="${CLI_DIR:-/usr/bin}"
    OC_TOOL="${CLI_DIR}"/oc
    echo $HYPERSHIFT_HOSTED_CLUSTER_KUBECONFIG
    label_worker_cnf "${HYPERSHIFT_HOSTED_CLUSTER_KUBECONFIG}"
else
    label_worker_cnf
fi