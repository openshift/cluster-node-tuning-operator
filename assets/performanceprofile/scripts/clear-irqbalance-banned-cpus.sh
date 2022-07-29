#!/usr/bin/env bash
set -euo pipefail
set -x

# const
SED="/usr/bin/sed"
# tunable - overridable for testing purposes
IRQBALANCE_CONF="${1:-/etc/sysconfig/irqbalance}"
CRIO_ORIG_BANNED_CPUS="${2:-/etc/sysconfig/orig_irq_banned_cpus}"

[ ! -f ${IRQBALANCE_CONF} ] && exit 0

${SED} -i '/^\s*IRQBALANCE_BANNED_CPUS\b/d' ${IRQBALANCE_CONF} || exit 0
echo "IRQBALANCE_BANNED_CPUS=" >> ${IRQBALANCE_CONF}

# we now own this configuration. But CRI-O has code to restore the configuration,
# and until it gains the option to disable this restore flow, we need to make
# the configuration consistent such as the CRI-O restore will do nothing.
if [ -n ${CRIO_ORIG_BANNED_CPUS} ] && [ -f ${CRIO_ORIG_BANNED_CPUS} ]; then
	true > ${CRIO_ORIG_BANNED_CPUS}
fi
