#!/bin/bash

set -e

# expect oc to be in PATH by default
OC_TOOL="${OC_TOOL:-oc}"

success=0
iterations=0
sleep_time=10
max_iterations=180 # results in 30 minute timeout

# Let's gibe the operator some time to do its work before we unpause the MCP (see below)
echo "[INFO] Waiting a bit for letting the operator do its work"
sleep 30

until [[ $success -eq 1 ]] || [[ $iterations -eq $max_iterations ]]
do

  echo "[INFO] Unpausing  MCPs"
  set +e
  mcps=$(${OC_TOOL} get mcp --no-headers -o custom-columns=":metadata.name")
  for mcp in $mcps
  do
      ${OC_TOOL} patch mcp "${mcp}" -p '{"spec":{"paused":false}}' --type=merge &> /dev/null
  done
  set -e

  echo "[INFO] Checking if MCP picked up the performance MC"
  # MC with new generated name
  mc_new="$(${OC_TOOL} get mcp worker-cnf -o jsonpath='{.spec.configuration.source[?(@.name=="50-performance-'$CLUSTER'")].name}')"
  # MC with old generated name
  mc_old="$(${OC_TOOL} get mcp worker-cnf -o jsonpath='{.spec.configuration.source[?(@.name=="performance-'$CLUSTER'")].name}')"
  # No output means that the new machine config wasn't picked by MCO yet
  if [ -z "${mc_new}" ] && [ -z "${mc_old}" ]
  then
    iterations=$((iterations + 1))
    iterations_left=$((max_iterations - iterations))
    echo "[INFO] Performance MC not picked up yet. $iterations_left retries left."
    sleep $sleep_time
    continue
  fi

  echo "[INFO] Checking if MCP is updated"
  if ! ${OC_TOOL} wait mcp/worker-cnf --for condition=Updated --timeout 1s &> /dev/null
  then
    iterations=$((iterations + 1))
    iterations_left=$((max_iterations - iterations))
    if [[ $iterations_left != 0  ]]; then
      echo "[WARN] MCP not updated yet, retrying in $sleep_time sec, $iterations_left retries left"
      sleep $sleep_time
    fi
  else
    success=1
  fi


done

if [[ $success -eq 0 ]]; then
  echo "[ERROR] MCP update failed, giving up."
  exit 1
fi

echo "[INFO] MCP update successful."
