#!/usr/bin/env bash

function set_queue_rps_mask() {
# replace x2d with hyphen (-) which is an escaped character
# that was added by systemd-escape in order to escape the systemd unit name that invokes this script
path=${path/x2d/-}
# set rps affinity for the queue
echo "${mask}"  2> /dev/null > "/sys/${path}/rps_cpus"
# we return 0 because the 'echo' command might fail if the device path to which the queue belongs has changed.
# this can happen in case of SRI-OV devices renaming.
return 0
}

function set_net_dev_rps_mask() {
  # in case of device we want to iterate through all queues
for i in /sys/"${path}"/queues/rx-*; do
  echo "${mask}" 2> /dev/null > "${i}/rps_cpus"
done
# we return 0 because the 'echo' command might fail if the device path to which the queue belongs has changed.
# this can happen in case of SRI-OV devices renaming.
return 0
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
