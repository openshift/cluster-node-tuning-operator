#!/usr/bin/env bash

JQ="/usr/bin/jq"
IP="/usr/sbin/ip"
WORKDIR=$(realpath "$(dirname "${BASH_SOURCE[0]}")")

mask="${1}"
[ -n "${mask}" ] || { logger "${0}: The rps-mask parameter is missing" ; exit 0; }

pid=$(${JQ} '.pid' /dev/stdin 2>&1)
[[ $? -eq 0 && -n "${pid}" ]] || { logger "${0}: Failed to extract the pid: ${pid}"; exit 0; }

ns=$(${IP} netns identify "${pid}" 2>&1)
[[ $? -eq 0 && -n "${ns}" ]] || { logger "${0} Failed to identify the namespace: ${ns}"; exit 0; }

# Updates the container veth RPS mask on the node
jlink=$(${IP} netns exec "${ns}" ${IP} -j link)
netns_link_indexes=$(${JQ} ".[] | select(.link_index != null) | .link_index" <<< ${jlink} | sort --unique)

for link_index in ${netns_link_indexes}; do
  container_veth=$(${IP} -j link | ${JQ} ".[] | select(.ifindex == ${link_index}) | .ifname" | tr -d '"')
  echo ${mask} > /sys/devices/virtual/net/${container_veth}/queues/rx-0/rps_cpus
done

# Updates the RPS mask for the interface inside of the container network namespace
if ! ${IP} netns exec "${ns}" ${WORKDIR}/set-ns-rps-mask.sh ${mask}; then
  exit 1
fi
