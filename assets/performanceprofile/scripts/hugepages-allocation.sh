#!/usr/bin/env bash

set -euo pipefail

nodes_path="/sys/devices/system/node"
hugepages_file="${nodes_path}/node${NUMA_NODE}/hugepages/hugepages-${HUGEPAGES_SIZE}kB/nr_hugepages"

if [ ! -f "${hugepages_file}" ]; then
  echo "ERROR: ${hugepages_file} does not exist"
  exit 1
fi

timeout=60
sample=1
current_time=0
while [ "$(cat "${hugepages_file}")" -ne "${HUGEPAGES_COUNT}" ]; do
  echo "${HUGEPAGES_COUNT}" >"${hugepages_file}"

  current_time=$((current_time + sample))
  if [ $current_time -gt $timeout ]; then
    echo "ERROR: ${hugepages_file} does not have the expected number of hugepages ${HUGEPAGES_COUNT}"
    exit 1
  fi

  sleep $sample
done
