# Disable IRQ load balancing globally or on-demand

<!-- toc -->
- [Summary](#summary)
- [Goals](#goals)
- [Design Details](#design-details)
- [Debugging](#debugging)
<!-- /toc -->

## Summary

In order to provide low-latency, exclusive CPU usage for guaranteed pods, the Performance Addons Operator can apply tuning
to remove all non-reserved CPUs from being eligible CPUs for processing device interrupts. 
Sometimes the reserved CPUs are not enough to handle networking device interrupts, in this case some isolated CPUs
will be needed to join the effort. It is done is by annotating the pods that have stricter RT requirements, removing
only the annotated pod's CPUs from the list of the CPUs that are allowed to handle device interrupts.

### Goals

- Provide a way to enable/disable device interrupts globally
- Allow disabling device interrupts only for specific pods when the device interrupts are not disabled globally
- Keep the existing behaviour for existing deployments (The device interrupts are disabled globally)

## Design Details

The Performance Profile CRD is promoted to 'v2', having a new optional boolean field ```GloballyDisableIrqLoadBalancing```
with default value ```false```. The Performance Addon Operator disables device interrupts on all isolated CPUs only
when ```GloballyDisableIrqLoadBalancing``` is set to ```true```.

Existing Performance Profile CRs with API versions 'v1' or 'v1alpha1' are converted to 'v2' using a Conversion Webhook
that injects the ```GloballyDisableIrqLoadBalancing``` field with the value ```true```.

When ```GloballyDisableIrqLoadBalancing``` is ```false```, the functionality to disable device interrupts on pod CPUs
it is implemented on the CRI-O level based on
- the pod using ***performance-<profile_name>*** runtime class
- the pod having ***irq-load-balancing.crio.io: true*** annotation
- the pod having ***cpu-quota.crio.io: true*** annotation

The Performance Addons Operator will be responsible for the creation of the high-performance runtime handler config snippet,
it will have the same content as default runtime handler, under relevant nodes, 
and for creation of the high-performance runtime class under the cluster.

A user will be responsible for specifying the relevant runtime class and annotation under the pod.

To disable device interrupts on pod CPUs, the pod specification will need to include the following fields:

```yaml
apiVersion: v1
kind: Pod
metadata:
  ...
  annotations:
    ...
    irq-load-balancing.crio.io: "disable"
    cpu-quota.crio.io: "disable"
    ...
  ... 
spec:
  ... 
  runtimeClassName: performance-<profile_name>
  ...
```

## Debugging
Here are the steps to ensure the system is configured correctly for IRQ dynamic load balancing

Consider a node with 6 CPUs targeted by a 'v2' [Performance Profile](performance_profile.md):
Let's assume the node name is ```cnf-worker.demo.lab```.

A profile reserving 2 CPUs for housekeeping can look like this:
```yaml
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: dynamic-irq-profile
spec:
  cpu:
    isolated: 2-5
    reserved: 0-1
  ...
```
1. Ensure you are using a v2 profile in the apiVersion.
2. Ensure ```GloballyDisableIrqLoadBalancing``` field is missing or has the value ```false```.

Start a pod configured as in [Design Details](#Design Details):
The pod below is guaranteed and requires 2 exclusive CPUs out of the 6 available CPUs in the node.
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: dynamic-irq-pod
  annotations:
     irq-load-balancing.crio.io: "disable"
     cpu-quota.crio.io: "disable"
spec:
  containers:
  - name: dynamic-irq-pod
    image: "quay.io/openshift-kni/cnf-tests:4.6"
    command: ["sleep", "10h"]
    resources:
      requests:
        cpu: 2
        memory: "200M"
      limits:
        cpu: 2
        memory: "200M"
  nodeSelector:
    node-role.kubernetes.io/worker-cnf: ""
  runtimeClassName: dynamic-irq-profile

```
1. Ensure both annotations exist.
2. Ensure the pod has its ```runtimeClassName``` as the respective profile name, in this case dynamic-irq-profile.
3. Ensure the node selector targets a cnf-worker.

Ensure the pod is running correctly.
```
oc get pod -o wide
NAME              READY   STATUS    RESTARTS   AGE     IP             NODE                  NOMINATED NODE   READINESS GATES
dynamic-irq-pod   1/1     Running   0          5h33m   10.135.1.140   cnf-worker.demo.lab   <none>           <none>
```
1. Ensure status is ```Running```.
2. Ensure the pod is scheduled on a cnf-worker node, in our case on the ```cnf-worker.demo.lab``` node.

Find out the CPUs dynamic-irq-pod runs on.
```
oc exec -it dynamic-irq-pod -- /bin/bash -c "grep Cpus_allowed_list /proc/self/status | awk '{print $2}'"
Cpus_allowed_list:	2-3
```

Ensure the node configuration is applied correctly.
Connect to the ```cnf-worker.demo.lab``` node to verify the configuration.
```
oc debug node/ocp47-worker-0.demo.lab
Starting pod/ocp47-worker-0demolab-debug ...
To use host binaries, run `chroot /host`

Pod IP: 192.168.122.99
If you don't see a command prompt, try pressing enter.

sh-4.4# 
```
Use the node file system:
```
sh-4.4# chroot /host
sh-4.4# 
```

1. Ensure the default system CPU affinity mask does not include the dynamic-irq-pod CPUs, in our case 2,3.
```   
cat /proc/irq/default_smp_affinity
33
```

2. Ensure the system IRQs are not configured to run on the dynamic-irq-pod CPUs
```
find /proc/irq/ -name smp_affinity_list -exec sh -c 'i="$1"; mask=$(cat $i); file=$(echo $i); echo $file: $mask' _ {} \;
/proc/irq/0/smp_affinity_list: 0-5
/proc/irq/1/smp_affinity_list: 5
/proc/irq/2/smp_affinity_list: 0-5
/proc/irq/3/smp_affinity_list: 0-5
/proc/irq/4/smp_affinity_list: 0
/proc/irq/5/smp_affinity_list: 0-5
/proc/irq/6/smp_affinity_list: 0-5
/proc/irq/7/smp_affinity_list: 0-5
/proc/irq/8/smp_affinity_list: 4
/proc/irq/9/smp_affinity_list: 4
/proc/irq/10/smp_affinity_list: 0-5
/proc/irq/11/smp_affinity_list: 0
/proc/irq/12/smp_affinity_list: 1
/proc/irq/13/smp_affinity_list: 0-5
/proc/irq/14/smp_affinity_list: 1
/proc/irq/15/smp_affinity_list: 0
/proc/irq/24/smp_affinity_list: 1
/proc/irq/25/smp_affinity_list: 1
/proc/irq/26/smp_affinity_list: 1
/proc/irq/27/smp_affinity_list: 5
/proc/irq/28/smp_affinity_list: 1
/proc/irq/29/smp_affinity_list: 0
/proc/irq/30/smp_affinity_list: 0-5
```
Note: Some IRQ controllers do not support IRQ re-balancing and will always expose all online CPUs as the IRQ mask.
Usually they will effectively run on CPU 0, a hint can be received with:
```
for i in {0,2,3,5,6,7,10,13,30}; do cat /proc/irq/$i/effective_affinity_list; done
0

0
0
0
0
0
0
1
```
