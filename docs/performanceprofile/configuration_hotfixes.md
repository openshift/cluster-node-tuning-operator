# Configuration Hotfixes

This is an advanced guide for changing low level performance configuration on a cluster to hotfix an issue or test impact.

Instructions for editing (add/remove/change) kernel arguments,sysfs,proc parameters and are described in this section.

## Default tunings

Default tunings are applied with the [openshift-performance](../../assets/performanceprofile/tuned/openshift-node-performance) base profile, it is the base for creating a Tuned CR that would be detected by the Node Tuning Operator and finally be executed by [tuned](https://github.com/redhat-performance/tuned).

## RPS settings

RPS (Receive Packet Steering) settings are now disabled by default for all performance profiles.\
When enabled via annotation, RPS settings configure the RPS mask as the [reserved CPUs](performance_profile.md#cpu),\
on the host level for all network devices excluding virtual(veth) devices and physical devices(pci)\
and on the container level for all virtual network devices(veth).
### Enabling RPS

To enable RPS settings, use the annotation `performance.openshift.io/enable-rps` with value `"true"` or `"enable"`:\

```yaml
performance_profile.yaml
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: example-performanceprofile
  annotations:
     performance.openshift.io/enable-rps: "enable"
spec:
  # ... rest of profile spec
```

You can also use `"true"` as the value:

```yaml
performance_profile.yaml
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: example-performanceprofile
  annotations:
     performance.openshift.io/enable-rps: "true"
spec:
  # ... rest of profile spec
```

The following configurations will result in no RPS settings applied on the cluster:

**Default behavior (no annotation):**
```yaml
performance_profile.yaml
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: example-performanceprofile
spec:
  # No annotation needed - RPS is disabled by default
  workloadHints:
    realTime: true
```

**Explicitly disabled with annotation:**
```yaml
performance_profile.yaml
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: example-performanceprofile
  annotations:
     performance.openshift.io/enable-rps: "disable"
spec:
  workloadHints:
    realTime: true
```

### Enable RPS on physical devices annotation

To enable RPS mask for physical(pci) devices as well on the host side, both RPS must be enabled and the physical device annotation must be set:

```yaml
performance_profile.yaml
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: example-performanceprofile
  annotations:
     performance.openshift.io/enable-rps: "enable"
     performance.openshift.io/enable-physical-dev-rps: "true"
```

> Note: `performance.openshift.io/enable-physical-dev-rps` annotation requires
`performance.openshift.io/enable-rps` to be set to an enabling value ("true" or "enable").

## Additional kernel arguments

When creating a [performance profile CR](../../examples/performanceprofile/samples/performance_v1_performanceprofile.yaml) , a default set of kernel arguments are created from the [openshift-performance](../../assets/performanceprofile/tuned/openshift-node-performance) base profile in addition to tuned generated argument and can include for example:

`nohz=on rcu_nocbs=<isolated_cores> tuned.non_isolcpus=<not_isolated_cpumask> intel_pstate=disable nosoftlockup tsc=nowatchdog intel_iommu=on iommu=pt systemd.cpu_affinity=<not_isolated_cores> isolcpus=<isolated_cores> default_hugepagesz=<DefaultHugepagesSize> hugepagesz=<hugepages_size> hugepages=<hugepages_>`

> Note: isolcpus is added only when [balanceIsolated](performance_profile.md#cpu) is disabled.

Additional kernel arguments could be added in the performance profile CR using the `additionalKernelArgs` field:

```yaml
apiVersion: performance.openshift.io/v1
kind: PerformanceProfile
metadata:
  name: example-performanceprofile
spec:
  additionalKernelArgs:
  - "nmi_watchdog=0"
  - "audit=0"
  - "mce=off"
  - "processor.max_cstate=1"
  - "idle=poll"
  - "intel_idle.max_cstate=0"  
...
```

> Note: These arguments will be added on top of the default arguments mentioned above. Editing these additional arguments could be done when editing the CR. 

> Note: This should be used for simple additions, for more complex operations see the following custom tunings section.

## Custom tunings 

To perform hotfixes on top of the tuned [openshift-performance](../../assets/performanceprofile/tuned/openshift-node-performance) base profile, a tuned custom profile (A child profile) will be used to apply the desired changes.
This profile will inherit the base tuned profile and override its fields where needed. 

For complete details about customizing tuned see : [Customizing Tuned profiles](https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux/8/html/monitoring_and_managing_system_status_and_performance/customizing-tuned-profiles_monitoring-and-managing-system-status-and-performance).

### Getting the current deployed tuned profile

In order to apply changes we will need to get the name of the deployed tuned profile that was generated by the Performance Profile Controller:

```
#oc describe performanceprofile <profile name> | grep Tuned
Tuned:  <tuned namespace>/<tuned name>

```
Any tuned profile created for custom tunings will need to inherit from this tuned profile: 

`include=<tuned name>`

The custom Tuned CR should be under the same tuned namespace:

```
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: ...
  namespace: <tuned namespace>
```

for example:

```yaml
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: configuration-hotfixes
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=...
      # override performance addons generated tuned profile
      include=openshift-node-performance-manual
```

### Example use cases

### Initial performance profile

```yaml
performance_profile.yaml
apiVersion: performance.openshift.io/v1
kind: PerformanceProfile
metadata:
  name: manual
spec:
  additionalKernelArgs:
    - "nmi_watchdog=0"
    - "audit=0"
    - "mce=off"
    - "processor.max_cstate=1"
    - "idle=poll"
    - "intel_idle.max_cstate=0"
  cpu:
    isolated: "1-3"
    reserved: "0"
  hugepages:
    defaultHugepagesSize: "1G"
    pages:
      - size: "1G"
        count: 1
        node: 0
  realTimeKernel:
    enabled: true
  numa:
    topologyPolicy: "single-numa-node"
  nodeSelector:
    node-role.kubernetes.io/worker-cnf: ""

```

Example of the kernel arguments generated after initial profile deployment:

`sh-4.2# cat /proc/cmdline
BOOT_IMAGE=(hd0,gpt1)/ostree/rhcos-35750ad692eb3cc24529d0bc23857ad3cc29340d39912b43e3a40d255f05f740/vmlinuz-4.18.0-147.8.1.rt24.101.el8_1.x86_64 rhcos.root=crypt_rootfs console=tty0 console=ttyS0,115200n8 rd.luks.options=discard ostree=/ostree/boot.1/rhcos/35750ad692eb3cc24529d0bc23857ad3cc29340d39912b43e3a40d255f05f740/0 ignition.platform.id=gcp skew_tick=1 nmi_watchdog=0 audit=0 mce=off processor.max_cstate=1 `**idle=poll**` intel_idle.max_cstate=0 nohz=on rcu_nocbs=1-3 tuned.non_isolcpus=00000001 intel_pstate=disable nosoftlockup default_hugepagesz=1G tsc=nowatchdog intel_iommu=on iommu=pt systemd.cpu_affinity=0`

>Note: check /proc/cmdline on the nodes to get the current kernel arguments list. 

#### Removing kernel argument

```yaml
oc create -f- <<_EOF_
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: configuration-hotfixes
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Configuration changes profile inherited from performance created tuned

      include=openshift-node-performance-manual
      [bootloader]
      cmdline_removeKernelArgs=-idle=poll
    name: openshift-configuration-hotfixes
  recommend:
  - machineConfigLabels:
      machineconfiguration.openshift.io/role: "worker-cnf"
    priority: 15
    profile: openshift-configuration-hotfixes
_EOF_


```

The kernel argument is now removed:

```sh

sh-4.2# cat /proc/cmdline | grep "idle=poll"
sh-4.2# 

```
#### Changing sysctl values

```sh
sh-4.2# sysctl -n kernel.hung_task_timeout_secs
600
sh-4.2# sysctl -n kernel.nmi_watchdog          
0
sh-4.2# sysctl -n kernel.sched_rt_runtime_us
-1

```

```yaml
oc create -f- <<_EOF_
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: configuration-hotfixes
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Configuration changes profile inherited from performance created tuned

      include=openshift-node-performance-manual
      [sysctl]
      kernel.hung_task_timeout_secs = 700  # change value from 600 to 700
      kernel.nmi_watchdog=     #set empty value
      kernel.sched_rt_runtime_us=-   # try removal
         

    name: openshift-configuration-hotfixes
  recommend:
  - machineConfigLabels:
      machineconfiguration.openshift.io/role: "worker-cnf"
    priority: 15
    profile: openshift-configuration-hotfixes
_EOF_

```

```sh
sh-4.2# sysctl -n kernel.hung_task_timeout_secs
700
sh-4.2# sysctl -n kernel.nmi_watchdog
0
sh-4.2# sysctl -n kernel.sched_rt_runtime_us
950000

```
