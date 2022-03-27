#!/bin/bash

# expect oc to be in PATH by default
OC_TOOL="${OC_TOOL:-oc}"

profiles=$(${OC_TOOL} get performanceprofile -o name)
for profileName in $profiles
do
  nodeSelector="$(${OC_TOOL} get $profileName -o=jsonpath='{.spec.nodeSelector}'  | awk -F'[/"]' '{print $3}')"

  if [[ $nodeSelector != "worker" ]]; then
    mcps+=($(${OC_TOOL} get mcp -l machineconfiguration.openshift.io/role=$nodeSelector -o name | awk -F "/" '{print $2}'))
    nodes=$(${OC_TOOL} get nodes --selector="node-role.kubernetes.io/${nodeSelector}" -o name)
    for node in $nodes
    do
        echo "[INFO]: Unlabeling node $node"
        ${OC_TOOL} label $node node-role.kubernetes.io/${nodeSelector}-
    done
  fi
done

# Give MCO some time to notice change
sleep 10

# Wait for worker MCP being updated
success=0
iterations=0
sleep_time=10
max_iterations=180 # results in 30 minute timeout
until [[ $success -eq 1 ]] || [[ $iterations -eq $max_iterations ]]
do
  echo "[INFO] Checking if MCP is updated"
  if ! ${OC_TOOL} wait mcp/worker --for condition=Updated --timeout 1s
  then
    iterations=$((iterations + 1))
    iterations_left=$((max_iterations - iterations))
    echo "[INFO] MCP not updated yet. $iterations_left retries left."
    sleep $sleep_time
    continue
  fi

  success=1

done

if [[ $success -eq 0 ]]; then
  echo "[ERROR] MCP update failed, going on nonetheless."
fi

# Delete CRs: this will undeploy all the MCs etc. (once it is implemented)
echo "[INFO] Deleting PerformanceProfile and giving the operator some time to undeploy everything"
$OC_TOOL delete performanceprofile --all
sleep 30

# Delete worker-cnf MCP
for mcp in "${mcps[@]}"
do
    echo "[INFO] Deleting MCP $mcp"
    $OC_TOOL delete mcp $mcp
done

