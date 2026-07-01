#!/bin/bash

# dedicated-cpus-configure.sh configures the dedicatedcpus.slice cpuset
# partition so that the dedicated CPUs are fully isolated from the kernel
# scheduler (equivalent to isolcpus=domain for those specific CPUs).

set -euo pipefail

DEDICATED_CPUS="{{ .DedicatedCpus }}"

if [ -z "$DEDICATED_CPUS" ]; then
	echo "No dedicated CPUs configured, nothing to do"
	exit 0
fi

CGROUP_PATH="/sys/fs/cgroup/dedicatedcpus.slice"

if [ ! -d "$CGROUP_PATH" ]; then
	echo "ERROR: dedicatedcpus.slice cgroup does not exist at $CGROUP_PATH" >&2
	exit 1
fi

# Set exclusive CPUs on the dedicated slice
echo "$DEDICATED_CPUS" > "$CGROUP_PATH/cpuset.cpus.exclusive"

# Set the CPUs available to the slice
echo "$DEDICATED_CPUS" > "$CGROUP_PATH/cpuset.cpus"

# Create an isolated partition — removes these CPUs from the parent's
# scheduling domain, giving kernel-level isolation equivalent to
# isolcpus=domain without affecting other CPUs.
echo "isolated" > "$CGROUP_PATH/cpuset.cpus.partition"

echo "Configured dedicatedcpus.slice as isolated partition for CPUs: $DEDICATED_CPUS"
