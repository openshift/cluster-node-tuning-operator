#!/bin/bash

set -euo pipefail

OVS_DPDK_CPUS="{{ .OvsDpdkCpus }}"
PARTITION_TYPE="{{ .PartitionType }}"

if [ -z "$OVS_DPDK_CPUS" ]; then
	echo "No OVS-DPDK CPUs configured, nothing to do"
	exit 0
fi

OVS_SLICE="/sys/fs/cgroup/ovs.slice"
VSWITCHD_CGROUP="${OVS_SLICE}/ovs-vswitchd.service"
OVSDPDK_SLICE="${VSWITCHD_CGROUP}/ovsdpdk.slice"

if [ ! -d "$OVS_SLICE" ]; then
	echo "ERROR: ovs.slice cgroup does not exist at $OVS_SLICE" >&2
	exit 1
fi

if [ ! -d "$VSWITCHD_CGROUP" ]; then
	echo "ERROR: ovs-vswitchd.service cgroup does not exist at $VSWITCHD_CGROUP" >&2
	exit 1
fi

echo "$OVS_DPDK_CPUS" > "$OVS_SLICE/cpuset.cpus.exclusive"
echo "+cpuset +cpu" > "$OVS_SLICE/cgroup.subtree_control"

echo "+cpuset +cpu +pids" > "$VSWITCHD_CGROUP/cgroup.subtree_control"

mkdir -p "$OVSDPDK_SLICE"

echo "threaded" > "$OVSDPDK_SLICE/cgroup.type"
echo "$OVS_DPDK_CPUS" > "$OVSDPDK_SLICE/cpuset.cpus"
echo "$PARTITION_TYPE" > "$OVSDPDK_SLICE/cpuset.cpus.partition"

echo "Configured ovsdpdk.slice inside ovs-vswitchd.service as partition=$PARTITION_TYPE for CPUs: $OVS_DPDK_CPUS"
