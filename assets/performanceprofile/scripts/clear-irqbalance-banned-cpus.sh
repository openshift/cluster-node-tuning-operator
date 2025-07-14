#!/usr/bin/env bash
set -euo pipefail
set -x

# const
SED="/usr/bin/sed"
# tunable - overridable for testing purposes
IRQBALANCE_CONF="${1:-/etc/sysconfig/irqbalance}"
CRIO_ORIG_BANNED_CPUS="${2:-/etc/sysconfig/orig_irq_banned_cpus}"
NONE=0

[ ! -f "${IRQBALANCE_CONF}" ] && exit 0

${SED} -i '/^\s*IRQBALANCE_BANNED_CPULIST\b/d' "${IRQBALANCE_CONF}" || exit 0
# A CPU list that represents the CPUs that will not have any irq's assigned to them on rebalance.
# '-' is a special value that means all CPUs are participating in load balancing (none is banned).
echo "IRQBALANCE_BANNED_CPULIST=-" >> "${IRQBALANCE_CONF}"

${SED} -i '/^\s*IRQBALANCE_BANNED_CPUS\b/d' "${IRQBALANCE_CONF}" || exit 0
# This variable is deprecated, but we need to keep it for backward compatibility.
# Set it to zero to be consistent with the new variable.
# This variable will be removed in the future.
echo "IRQBALANCE_BANNED_CPUS=${NONE}" >> "${IRQBALANCE_CONF}"

