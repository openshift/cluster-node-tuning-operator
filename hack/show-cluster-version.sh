#!/bin/bash

# expect oc to be in PATH by default
OC_TOOL="${OC_TOOL:-oc}"

echo "Cluster version"
${OC_TOOL} version || :
${OC_TOOL} get nodes -o custom-columns=VERSION:.status.nodeInfo.kubeletVersion || :
${OC_TOOL} get clusterversion || :
