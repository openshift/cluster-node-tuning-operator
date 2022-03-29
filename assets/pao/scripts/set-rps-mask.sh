#!/usr/bin/env bash

dev=$1
[ -n "${dev}" ] || { echo "The device argument is missing" >&2 ; exit 1; }

mask=$2
[ -n "${mask}" ] || { echo "The mask argument is missing" >&2 ; exit 1; }

dev_dir="/sys/class/net/${dev}"

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

[ -d "${dev_dir}" ] || find_dev_dir                # the net device was renamed, find the new name
[ -d "${dev_dir}" ] || { sleep 5; find_dev_dir; }  # search failed, wait a little and try again
[ -d "${dev_dir}" ] || { echo "${dev_dir}" directory not found >&2 ; exit 0; } # the interface disappeared, not an error

find "${dev_dir}"/queues -type f -name rps_cpus -exec sh -c "echo ${mask} | cat > {}" \;