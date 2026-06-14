#!/usr/bin/env bash
set -euo pipefail
set -x

# const
SED="/usr/bin/sed"
# tunable - overridable for testing purposes
IRQBALANCE_CONF="${1:-/etc/sysconfig/irqbalance}"
CRIO_ORIG_BANNED_CPUS="${2:-/etc/sysconfig/orig_irq_banned_cpus}"
NONE=0

# BANNED_CPUS: the final hex mask written to IRQBALANCE_BANNED_CPUS.
#   1. If DEDICATED_CPUS is set (via systemd Environment), use it.
#   2. Otherwise, default to 0 (no CPUs banned, all participate in balancing).
if [ -n "${DEDICATED_CPUS:-}" ]; then
	BANNED_CPUS="${DEDICATED_CPUS}"
else
	BANNED_CPUS="${NONE}"
fi

[ ! -f "${IRQBALANCE_CONF}" ] && exit 0

${SED} -i '/^\s*IRQBALANCE_BANNED_CPUS\b/d' "${IRQBALANCE_CONF}" || exit 0
# CPU numbers which have their corresponding bits set to one in this mask
# will not have any irq's assigned to them on rebalance.
# so zero means all cpus are participating in load balancing.
echo "IRQBALANCE_BANNED_CPUS=${BANNED_CPUS}" >> "${IRQBALANCE_CONF}"

# we now own this configuration. But CRI-O has code to restore the configuration,
# and until it gains the option to disable this restore flow, we need to make
# the configuration consistent such as the CRI-O restore will do nothing.
if [ -n "${CRIO_ORIG_BANNED_CPUS}" ] && [ -f "${CRIO_ORIG_BANNED_CPUS}" ]; then
	echo "${BANNED_CPUS}" > "${CRIO_ORIG_BANNED_CPUS}"
fi

# CRI-O reads /proc/irq/default_smp_affinity to derive the IRQ banned mask
# when pods with irq-load-balancing.crio.io=disable are scheduled.
# If we don't remove the dedicated CPUs from default_smp_affinity here,
# CRI-O will overwrite IRQBALANCE_BANNED_CPUS while ignoring the dedicated CPUs.
SMP_AFFINITY="/proc/irq/default_smp_affinity"
if [ "${BANNED_CPUS}" != "${NONE}" ] && [ -f "${SMP_AFFINITY}" ]; then
	# default_smp_affinity is comma-separated 32-bit hex groups (e.g. "ff,ffffffff,ffffffff")
	IFS=',' read -ra smp < "${SMP_AFFINITY}"
	n=${#smp[@]}
	# pad BANNED_CPUS with leading zeros to match the same number of hex chars
	padded="${BANNED_CPUS}"
	while [ ${#padded} -lt $(( n * 8 )) ]; do
		padded="0${padded}"
	done
	# clear banned bits from each 32-bit group: result = smp & ~banned
	result=""
	for (( i=0; i<n; i++ )); do
		ban="${padded:$(( i * 8 )):8}"
		val=$(printf "%08x" $(( 0x${smp[$i]} & ~0x${ban} )))
		result+="${result:+,}${val}"
	done
	echo "Setting default_smp_affinity to ${result} (removing dedicated CPUs ${BANNED_CPUS} from default_smp_affinity mask)"
	echo "${result}" > "${SMP_AFFINITY}"
fi
