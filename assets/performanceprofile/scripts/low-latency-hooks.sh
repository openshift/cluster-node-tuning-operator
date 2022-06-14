#!/usr/bin/env bash

JQ="/usr/bin/jq"
IP="/usr/sbin/ip"

mask="${1}"
[ -n "${mask}" ] || { logger "${0}: The rps-mask parameter is missing" ; exit 0; }

pid=$(${JQ} '.pid' /dev/stdin 2>&1)
[[ $? -eq 0 && -n "${pid}" ]] || { logger "${0}: Failed to extract the pid: ${pid}"; exit 0; }

ns=$(${IP} netns identify "${pid}" 2>&1)
[[ $? -eq 0 && -n "${ns}" ]] || { logger "${0} Failed to identify the namespace: ${ns}"; exit 0; }

# Updates the container veth RPS mask on the node
netns_link_indexes=$(${IP} netns exec "${ns}" ${IP} -j link | ${JQ} ".[] | select(.link_index != null) | .link_index")
for link_index in ${netns_link_indexes}; do
  container_veth=$(${IP} -j link | ${JQ} ".[] | select(.ifindex == ${link_index}) | .ifname" | tr -d '"')
  echo ${mask} > /sys/devices/virtual/net/${container_veth}/queues/rx-0/rps_cpus
done

# Updates the RPS mask for the interface inside of the container network namespace
mode=$(${IP} netns exec "${ns}" [ -w /sys ] && echo "rw" || echo "ro" 2>&1)
[ $? -eq 0 ] || { logger "${0} Failed to determine if the /sys is writable: ${mode}"; exit 0; }

if [ "${mode}" = "ro" ]; then
    res=$(${IP} netns exec "${ns}" mount -o remount,rw /sys 2>&1)
    [ $? -eq 0 ] || { logger "${0}: Failed to remount /sys as rw: ${res}"; exit 0; }
fi

# /sys/class/net can't be used recursively to find the rps_cpus file, use /sys/devices/virtual instead
res=$(${IP} netns exec "${ns}" find /sys/devices/virtual -type f -name rps_cpus -exec sh -c "echo ${mask} | cat > {}" \; 2>&1)
[[ $? -eq 0 && -z "${res}" ]] || logger "${0}: Failed to apply the RPS mask: ${res}"

if [ "${mode}" = "ro" ]; then
    ${IP} netns exec "${ns}" mount -o remount,ro /sys
    [ $? -eq 0 ] || exit 1 # Error out so the pod will not start with a writable /sys
fi
