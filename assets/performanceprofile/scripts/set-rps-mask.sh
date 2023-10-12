#!/usr/bin/env bash

path=${1}
[ -n "${path}" ] || { echo "The device path argument is missing" >&2 ; exit 1; }

mask=${2}
[ -n "${mask}" ] || { echo "The mask argument is missing" >&2 ; exit 1; }

queue_path=${path%rx*}

queue_num=${path#*queues/}
# replace '/' with '-'
queue_num="${queue_num/\//-}"

# set rps affinity for the queue
echo "${mask}" > "/sys${queue_path}${queue_num}/rps_cpus"