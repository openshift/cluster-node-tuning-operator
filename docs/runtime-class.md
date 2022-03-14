# Performance profile runtime class

The performance-addon-operators for each profile creates the runtime class that you can use under the pod specification. The runtime class allows you to configure a set of annotations:
- `cpu-load-balancing.crio.io: disable` - will disable the CPU load balancing for CPUs used by the container.
- `cpu-quota.crio.io: disable` - will disable the CPU CFS quota for CPUs used by the container.
- `irq-load-balancing.crio.io: disable` - will disable IRQ load balancing for CPUs used by the container.

The runtime will configure the container CPUs only when the pod has guaranteed QoS class and requested whole CPUs.

Pod example:

```yaml
apiVersion: v1
kind: Pod
metadata:
  ...
  annotations:
    cpu-quota.crio.io: disable
    cpu-load-balancing.crio.io: disable
    irq-load-balancing.crio.io: disable
    ...
  ... 
spec:
  ... 
  runtimeClassName: performance-<profile_name>
  ...
```