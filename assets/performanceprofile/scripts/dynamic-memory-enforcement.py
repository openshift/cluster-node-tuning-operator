#!/usr/bin/env python3
# Dynamic Memory Enforcement Service
# This service is used to protect the cluster from memory exhaustion.
# It is used to set the memory.max value for the kubepods-burstable.slice and kubepods-besteffort.slice.

from datetime import datetime
import os
import os.path

ROOT="/sys/fs/cgroup/kubepods.slice"
ROOT_MAX=os.path.join(ROOT, "memory.max")

BESTEFFORT_MAX=os.path.join(ROOT, "kubepods-besteffort.slice", "memory.max")

BURSTABLE_CURRENT=os.path.join(ROOT, "kubepods-burstable.slice", "memory.current")
BURSTABLE_MAX=os.path.join(ROOT, "kubepods-burstable.slice", "memory.max")

def guaranteed_max():
    sum = 0
    
    for d in os.listdir(ROOT):
        if not d.startswith("kubepods-pod"):
            continue
        try:
            sum += int(open(os.path.join(ROOT, d, "memory.max")).read().strip())
        except IOError as e:
            continue

    return sum

if __name__ == "__main__":
    root_max = int(open(ROOT_MAX).read().strip())
    burstable_current = int(open(BURSTABLE_CURRENT).read().strip())
    guaranteed_current = guaranteed_max()

    burstable_max = root_max - guaranteed_current
    besteffort_max = burstable_max - burstable_current

    now = datetime.now().isoformat(' ', timespec='microseconds')
    print(f"\n{now}\n")
    print(f"root: {root_max}\nburstable: {burstable_current}\nguaranteed: {guaranteed_current}\n\nburstable max: {burstable_max}\nbest-effort max: {besteffort_max}\n")
    
    with open(BURSTABLE_MAX, "w") as f:
        f.write(str(burstable_max))

    with open(BESTEFFORT_MAX, "w") as f:
        f.write(str(besteffort_max))

