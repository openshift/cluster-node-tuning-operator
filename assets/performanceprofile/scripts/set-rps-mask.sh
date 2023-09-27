#!/usr/bin/env bash

full_path=${1}
[ -n "${full_path}" ] || { echo "The full device path argument is missing" >&2 ; exit 1; }

mask=${2}
[ -n "${mask}" ] || { echo "The mask argument is missing" >&2 ; exit 1; }

# replace '-' with '/'
de_escape_path="/sys${full_path//-//}"

# get the path for the queues
queues_path=${de_escape_path%/rx*}

# set rps affinity for all queues
for i in "${queues_path}"/rx-*/rps_cpus; do
  echo "${mask}" > "${i}"
done
