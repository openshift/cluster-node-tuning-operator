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

${SED} -i '/^\s*IRQBALANCE_BANNED_CPUS\b/d' "${IRQBALANCE_CONF}" || exit 0
# CPU numbers which have their corresponding bits set to one in this mask
# will not have any irq's assigned to them on rebalance.
# so zero means all cpus are participating in load balancing.
echo "IRQBALANCE_BANNED_CPUS=${NONE}" >> "${IRQBALANCE_CONF}"

# we now own this configuration. But CRI-O has code to restore the configuration,
# and until it gains the option to disable this restore flow, we need to make
# the configuration consistent such as the CRI-O restore will do nothing.
if [ -n "${CRIO_ORIG_BANNED_CPUS}" ] && [ -f "${CRIO_ORIG_BANNED_CPUS}" ]; then
	echo "${NONE}" > "${CRIO_ORIG_BANNED_CPUS}"
fi
