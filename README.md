# Node Tuning Operator

The Node Tuning Operator (NTO) manages cluster node-level tuning for
[OpenShift](https://openshift.io/).

The majority of high-performance applications require some
level of kernel tuning. The Operator provides a unified
management interface to users of node-level sysctls and more
flexibility to add [custom tuning](#custom-tuning-specification)
specified by user needs. The Operator manages the containerized
[Tuned](https://github.com/redhat-performance/tuned/)
daemon for [OpenShift](https://openshift.io/) as
a Kubernetes DaemonSet. It ensures [custom tuning
specification](#custom-tuning-specification) is passed to all
containerized Tuned daemons running in the cluster in the format
that the daemons understand. The daemons run on all nodes in the
cluster, one per node.

Node-level settings applied by the containerized Tuned daemon are rolled back
on an event that triggers a profile change or when the containerized Tuned
daemon is terminated gracefully by receiving and handling a termination signal.

## Deploying the Node Tuning Operator

The Operator is deployed by applying the `*.yaml` manifests in the Operator's
`/manifests` directory in alphanumeric order. It automatically creates a default deployment
and custom resource
([CR](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/))
for the Tuned daemons. The following shows the default CR created
by the Operator on a cluster with the Operator installed.

```
$ oc get Tuned -n openshift-cluster-node-tuning-operator
NAME      AGE
default   1h
```

The default CR is meant for delivering standard node-level tuning for
the OpenShift platform and it can only be modified to set the Operator
Management state. Any other custom changes to the default CR will be
overwritten by the Operator. For custom tuning, create your own Tuned CRs.
Newly created CRs will be combined with the default CR and custom tuning
applied to OpenShift nodes based on node or pod labels and profile priorities.

While in certain situations the support for pod labels can be a convenient
way of automatically delivering required tuning, this practice is discouraged
and strongly advised against especially in large-scale clusters. The default
Tuned CR ships without pod label matching. If a custom profile is created
with pod label matching the functionality will be enabled at that time.


## Custom tuning specification

For an example of a tuning specification, refer to
`/assets/tuned/manifests/default-cr-tuned.yaml` in the Operator's directory or to
the resource created in a live cluster by:

```
$ oc get Tuned/default -o yaml
```

The CR for the Operator has two major sections. The first
section, `profile:`, is a list of Tuned profiles and their names. The
second, `recommend:`, defines the profile selection logic.

Multiple custom tuning specifications can co-exist as multiple CRs
in the Operator's namespace. The existence of new CRs or the deletion
of old CRs is detected by the Operator. All existing custom tuning
specifications are merged and appropriate objects for the containerized
Tuned daemons are updated.

### Management state

The Operator Management state is set by adjusting the default Tuned CR.
By default, the Operator is in the Managed state and the `spec.managementState`
field is not present in the default Tuned CR. Valid values for the Operator
Management state are as follows:

  * Managed: the Operator will update its operands as configuration resources are updated
  * Unmanaged: the Operator will ignore changes to the configuration resources
  * Removed: the Operator will remove its operands and resources the Operator provisioned


### Profile data

The `profile:` section lists Tuned profiles and their names.

```
  profile:
  - name: tuned_profile_1
    data: |
      # Tuned profile specification
      [main]
      summary=Description of tuned_profile_1 profile

      [sysctl]
      net.ipv4.ip_forward=1
      # ... other sysctl's or other Tuned daemon plug-ins supported by the containerized Tuned

  # ...

  - name: tuned_profile_n
    data: |
      # Tuned profile specification
      [main]
      summary=Description of tuned_profile_n profile

      # tuned_profile_n profile settings
```

Refer to a list of
(Tuned plug-ins supported by the Operator)[#supported-tuned-daemon-plug-ins].


### Recommended profiles

The `profile:` selection logic is defined by the `recommend:` section of the CR.
The `recommend:` section is a list of items to recommend the profiles based on
a selection criteria.

```
  recommend:
  <recommend-item-1>
  # ...
  <recommend-item-n>
```

The individual items of the list:

```
  - machineConfigLabels:                # optional
      <mcLabels>                        # a dictionary of key/value MachineConfig labels; the keys must be unique
    match:                              # optional; if omitted, profile match is assumed unless a profile with a higher priority matches first or 'machineConfigLabels' is set
    <match>                             # an optional list
    priority: <priority>                # profile ordering priority, lower numbers mean higher priority (0 is the highest priority)
    profile: <tuned_profile_name>       # a Tuned profile to apply on a match; for example tuned_profile_1
    operand:				# optional operand configuration
      debug: <bool>			# turn debugging on/off for the Tuned daemon: true/false (default is false)
```

If `<match>` is omitted, a profile match (i.e. _true_) is assumed.

`<match>` is an optional list recursively defined as follows:

```
    - label: <label_name>     # node or pod label name
      value: <label_value>    # optional node or pod label value; if omitted, the presence of <label_name> is enough to match
      type: <label_type>      # optional node or pod type ("node" or "pod"); if omitted, "node" is assumed
      <match>                 # an optional <match> list
```

If `<match>` is not omitted, all nested `<match>` sections must
also evaluate to _true_. Otherwise, _false_ is assumed and the
profile with the respective `<match>` section will not be applied or
recommended. Therefore, the nesting (child `<match>` sections) works as logical
AND operator. Conversely, if any item of the `<match>` list matches,
the entire `<match>` list evaluates to _true_. Therefore, the list
acts as logical OR operator.

If `machineConfigLabels` is defined, MachineConfigPool based matching is turned on
for the given `recommend:` list item. `<mcLabels>` specifies the labels
for a MachineConfig. The MachineConfig is created automatically to apply host settings, such as
kernel boot parameters, for the profile `<tuned_profile_name>`. This involves
finding all MachineConfigPools with machineConfigSelector matching
`<mcLabels>` and setting the profile `<tuned_profile_name>` on all nodes that
match the MachineConfigPools' `nodeSelectors`.

The list items `match` and `machineConfigLabels` are connected by the logical OR operator.
The `match` item is evaluated first in a short-circuit manner. Therefore, if it evaluates to
`true`, `machineConfigLabels` item is not considered.


#### Example

```
  - match:
    - label: tuned.openshift.io/elasticsearch
      match:
      - label: node-role.kubernetes.io/master
      - label: node-role.kubernetes.io/infra
      type: pod
    priority: 10
    profile: openshift-control-plane-es
  - match:
    - label: node-role.kubernetes.io/master
    - label: node-role.kubernetes.io/infra
    priority: 20
    profile: openshift-control-plane
  - priority: 30
    profile: openshift-node
```

The CR above is translated for the containerized Tuned daemon into
its recommend.conf file based on the profile priorities. The profile
with the highest priority (10) is openshift-control-plane-es and,
therefore, it is considered first. The containerized Tuned daemon
running on a given node looks to see if there is a pod running on the
same node with the tuned.openshift.io/elasticsearch label set. If not,
the entire `<match>` section evaluates as _false_. If there is such a
pod with the label, in order for the `<match>` section to evaluate to
_true_, the node label also needs to be node-role.kubernetes.io/master
OR node-role.kubernetes.io/infra.

If the labels for the profile with priority 10 matched,
openshift-control-plane-es profile is applied and no other profile is
considered. If the node/pod label combination did not match,
the second highest priority profile (openshift-control-plane) is considered.
This profile is applied if the containerized Tuned pod runs on a node with
labels node-role.kubernetes.io/master OR node-role.kubernetes.io/infra.

Finally, the profile `openshift-node` has the lowest priority of 30.
It lacks the `<match>` section and, therefore, will always match. It
acts as a profile catch-all to set openshift-node profile, if no other
profile with higher priority matches on a given node.

### Example

The following CR applies custom node-level tuning for
OpenShift nodes that run an ingress pod with label
`tuned.openshift.io/ingress-pod-label=ingress-pod-label-value`.
As an administrator, use the following command to create a custom Tuned CR.

```
oc create -f- <<_EOF_
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: ingress
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=A custom OpenShift ingress profile
      include=openshift-control-plane
      [sysctl]
      net.ipv4.ip_local_port_range="1024 65535"
      net.ipv4.tcp_tw_reuse=1
    name: openshift-ingress
  recommend:
  - match:
    - label: tuned.openshift.io/ingress-pod-label
      value: "ingress-pod-label-value"
      type: pod
    priority: 10
    profile: openshift-ingress
_EOF_
```


## Supported Tuned daemon plug-ins

Aside from the `[main]` section, the following
[Tuned plug-ins](https://github.com/redhat-performance/tuned/tree/master/tuned/plugins)
are supported when using [custom profiles](#custom-tuning-specification) defined
in the `profile:` section of the Tuned CR:

* audio
* cpu
* disk
* eeepc_she
* modules
* mounts
* net
* rtentsk
* scheduler
* scsi_host
* selinux
* service
* sysctl
* sysfs
* systemd
* usb
* video
* vm

with the exception of dynamic tuning functionality provided by some of the plug-ins.
The following Tuned plug-ins are currently not fully supported:

* bootloader
* script


## Additional tuning on fully-managed hosts
Support for the [stall daemon](https://github.com/bristot/stalld)
(stalld) has been added to complement tuning performed by Tuned realtime
profiles. Currently, only hosts fully-managed by the
[Machine Config Operator](https://github.com/openshift/machine-config-operator)
(MCO) can benefit from this functionality. To deploy stalld on such hosts,
the following line needs to be added to the Tuned service plugin.

```
service.stalld=start,enable
```

A host-supplied configuration file can be used to override the stalld systemd
unit created when using the line above in the Tuned service plugin. This
file can then be referred to by prefixing the absolute path to the overlay
file on the host by the `/host` prefix.

```
service.stalld=start,enable,file:/host<absolute_path_to_the_overlay_file_on_the_host>
```
