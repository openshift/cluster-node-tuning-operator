#!/bin/bash

# cpuset-configure.sh configures three cpusets in preparation for allowing containers to have cpu load balancing disabled.
# To configure a cpuset to have load balance disabled (on cgroup v1), a cpuset cgroup must have `cpuset.sched_load_balance`
# set to 0 (disable), and any cpuset that contains the same set as `cpuset.cpus` must also have `cpuset.sched_load_balance` set to disabled.

set -euo pipefail

root=/sys/fs/cgroup/cpuset
system="$root"/system
machine="$root"/machine.slice

ovsslice="${root}/{{ .OvsSliceName }}"
ovsslice_systemd="/sys/fs/cgroup/pids/{{ .OvsSliceName }}"

# As such, the root cgroup needs to have cpuset.sched_load_balance=0. 
echo 0 > "$root"/cpuset.sched_load_balance

# However, this would present a problem for system daemons, which should have load balancing enabled.
# As such, a second cpuset must be created, here dubbed `system`, which will take all system daemons.
# Since systemd starts its children with the cpuset it is in, moving systemd will ensure all processes systemd begins will be in the correct cgroup.
mkdir -p "$system"
# cpuset.mems must be initialized or processes will fail to be moved into it.
cat "$root/cpuset.mems" > "$system"/cpuset.mems
# Retrieve the cpuset of systemd, and write it to cpuset.cpus of the system cgroup.
reserved_set=$(taskset -cp  1  | awk 'NF{ print $NF }')
echo "$reserved_set" > "$system"/cpuset.cpus

# And move the system processes into it.
# Note, some kernel threads will fail to be moved with "Invalid Argument". This should be ignored.
for process in $(cat "$root"/cgroup.procs | sort -r); do
	echo $process > "$system"/cgroup.procs 2>&1 | grep -v "Invalid Argument" || true;
done

# Finally, a the `machine.slice` cgroup must be preconfigured. Podman will create containers and move them into the `machine.slice`, but there's
# no way to tell podman to update machine.slice to not have the full set of cpus. Instead of disabling load balancing in it, we can pre-create it.
# with the reserved CPUs set ahead of time, so when isolated processes begin, the cgroup does not have an overlapping cpuset between machine.slice and isolated containers.
mkdir -p "$machine"

# It's unlikely, but possible, that this cpuset already existed. Iterate just in case.
for file in $(find "$machine" -name cpuset.cpus | sort -r); do echo "$reserved_set" > "$file"; done

# OVS is running in its own slice that spans all cpus. The real affinity is managed by OVN-K ovnkube-node daemonset
# Make sure this slice will not enable cpu balancing for other slice configured by this script.
# This might seem counter-intuitive, but this will actually NOT disable cpu balancing for OVS itself.
# - OVS has access to reserved cpus, but those have balancing enabled via the `system` cgroup created above
# - OVS has access to isolated cpus that are currently not assigned to pinned pods. Those have balancing enabled by the
#   pods running there (burstable and best-effort pods have balancing enabled in the container cgroup and access to all
#   unpinned cpus).

# systemd does not manage the cpuset cgroup controller, so move everything from the managed pids controller's ovs.slice
# to the cpuset controller.

# Create the ovs.slice
mkdir -p "$ovsslice"
echo 0 > "$ovsslice"/cpuset.sched_load_balance
cat "$root"/cpuset.cpus > "$ovsslice"/cpuset.cpus
cat "$root"/cpuset.mems > "$ovsslice"/cpuset.mems

# Move OVS over
for process in $(cat "$ovsslice_systemd"/*/cgroup.procs | sort -r); do
        echo $process > "$ovsslice"/cgroup.procs 2>&1 | grep -v "Invalid Argument" || true;
done