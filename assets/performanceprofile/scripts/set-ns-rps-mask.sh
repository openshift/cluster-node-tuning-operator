#!/usr/bin/env bash

MASK=${1}

if ! [[ -w /sys ]]; then
  if ! res=$(mount -o remount,rw /sys 2>&1); then
    logger "${0}: Failed to remount /sys as rw: ${res}"; exit 0;
  fi
fi

# /sys/class/net can't be used recursively to find the rps_cpus file, use /sys/devices/virtual instead
for dev in /sys/devices/virtual/net/*; do
          echo ${MASK} > ${dev}/queues/rx-0/rps_cpus || logger "${0}: Failed to apply the RPS mask on ${dev}:"
done
