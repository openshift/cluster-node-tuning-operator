#!/bin/bash

set -e

# expect oc to be in PATH by default
OC_TOOL="${OC_TOOL:-oc}"

# Label 1 worker node
echo "[INFO]: Labeling 1 worker node with worker-cnf"
node=$(${OC_TOOL} get nodes --selector='node-role.kubernetes.io/worker' \
    --selector='!node-role.kubernetes.io/master' -o name | head -1)

${OC_TOOL} label --overwrite $node node-role.kubernetes.io/worker-cnf=""
