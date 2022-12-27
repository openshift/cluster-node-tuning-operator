#!/usr/bin/env bash

instance_name=$1
[ -n "${instance_name}" ] || { echo "The instance name argument is missing" >&2 ; exit 1; }

mask=$2
[ -n "${mask}" ] || { echo "The mask argument is missing" >&2 ; exit 1; }

# this function is parsing the device name and store it in dev
# the pattern should look similar to -devices-virtual-net-<dev>-queues-rx-<n>
function parse_dev_name() {
 local tmp=${1#*net/}
 dev=${tmp%/queues*}
}

function find_dev_dir {
  systemd_devs=$(systemctl list-units -t device | grep sys-subsystem-net-devices | cut -d' ' -f1)

  for systemd_dev in ${systemd_devs}; do
    dev_sysfs=$(systemctl show "${systemd_dev}" -p SysFSPath --value)

    dev_orig_name="${dev_sysfs##*/}"
    if [ "${dev_orig_name}" = "${dev}" ]; then
      dev_name="${systemd_dev##*-}"
      dev_name="${dev_name%%.device}"
      if [ "${dev_name}" = "${dev}" ]; then # disregard the original device unit
              continue
      fi

      echo "${dev} device was renamed to $dev_name"
      dev_dir="/sys/class/net/${dev_name}"
      break
    fi
  done
}

parse_dev_name "${instance_name}"
[ -n "${dev}" ] || { echo failed to parse device name from "${instance_name}" >&2 ; exit 0; }

dev_dir="/sys/class/net/${dev}"

[ -d "${dev_dir}" ] || find_dev_dir                # the net device was renamed, find the new name
[ -d "${dev_dir}" ] || { sleep 5; find_dev_dir; }  # search failed, wait a little and try again
[ -d "${dev_dir}" ] || { echo "${dev_dir}" directory not found >&2 ; exit 0; } # the interface disappeared, not an error

for i in "${dev_dir}"/queues/rx-*/rps_cpus; do
  echo "${mask}" > "${i}"
done
