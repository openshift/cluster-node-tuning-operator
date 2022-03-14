# Disable CPU load balancing on-demand

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
- [Design Details](#design-details)
<!-- /toc -->

## Summary

5G rollout is a hot topic world-wide. Strong motivation stems from the requirement for the utmost 
5G high-performance of packet processing applications.

Kubernetes is a common option for 5G deployment, but it hardly can satisfy latency requirements defined by Telco operators.
The only way to achieve latency requirements is to disable the CPU load balancing for CPUs used by the container.
This was traditionally done with isolcpus kernel argument, but that "locks" those CPUs into no load-balancing
and is incompatible with worker nodes which have any pods which don't self pin all of their own software threads.

So the kubernetes needs to provide some way to disable the CPU load balancing for some pods under the environment.

## Motivation

To get the lowest latency in a container, that will satisfy Telco operators requirements. 

### Goals

- Provide the way to disable CPU load balancing for some pods on-demand.

## Design Details

Functionality to disable/enable the CPU load balancing will be implemented on the CRI-O level, 
the code under the CRI-O will disable/enable CPU load balancing only when:

- the pod uses ***performance-<profile_name>*** runtime class
- the pod has ***cpu-load-balancing.crio.io: disable*** annotation

The performance-addon-operator will be responsible for the creation of the high-performance runtime handler config snippet,
it will have the same content as default runtime handler, under relevant nodes, 
and for creation of the high-performance runtime class under the cluster.

A user will be responsible for specifying the relevant runtime class and annotation under the pod.

To disable the CPU load balancing for the pod, the pod specification will need to include the following fields:

```yaml
apiVersion: v1
kind: Pod
metadata:
  ...
  annotations:
    ...
    cpu-load-balancing.crio.io: "disable"
    ...
  ... 
spec:
  ... 
  runtimeClassName: performance-<profile_name>
  ...
```

---
**NOTE**: it important to be aware that disabling CPU load balancing should be done only, 
when the CPU manager static policy enabled and for pods with guaranteed QoS and that use whole CPUs,
otherwise, it can affect the performance of other containers in the cluster.
---
