#!/usr/bin/bash

set -euo pipefail

for cpu in ${OFFLINE_CPUS//,/ };
  do
    online_cpu_file="/sys/devices/system/cpu/cpu$cpu/online"
    if [ ! -f "${online_cpu_file}" ]; then
      echo "ERROR: ${online_cpu_file} does not exist, abort script execution"
      exit 1
    fi
  done

echo "All cpus offlined exists, set them offline"

for cpu in ${OFFLINE_CPUS//,/ };
  do
    online_cpu_file="/sys/devices/system/cpu/cpu$cpu/online"
    echo 0 > "${online_cpu_file}"
    echo "offline cpu num $cpu"
  done

