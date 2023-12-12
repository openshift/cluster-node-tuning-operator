#!/usr/bin/env bash

function set_queue_rps_mask() {
queue_path=${path%rx*}

queue_num=${path#*queues/}

# replace '/' with '-'
queue_num="${queue_num/\//-}"

# set rps affinity for the queue
echo "${mask}"  2> /dev/null > "/sys${queue_path}${queue_num}/rps_cpus"

# the 'echo' command might failed if the device path which the queue belongs to has changes
# this can happen in case of SRI-OV devices renaming
exit 0
}

function set_net_dev_rps_mask() {
  # in case of device we want to iterate through all queues
   for i in /sys"${path}"/queues/rx-*; do
     echo "${mask}" > "${i}/rps_cpus"
  done
 }

 path=${1}
 [ -n "${path}" ] || { echo "The device path argument is missing" >&2 ; exit 1; }

 mask=${2}
 [ -n "${mask}" ] || { echo "The mask argument is missing" >&2 ; exit 1; }

 if [[ "${path}" =~ "queues" ]]; then
   set_queue_rps_mask
 else
   set_net_dev_rps_mask
 fi

