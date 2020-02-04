# Node Tuning Operator

The Node Tuning Operator manages cluster node-level tuning for
[OpenShift](https://openshift.io/).

The majority of high-performance applications require some
level of kernel tuning.  The operator provides a unified
management interface to users of node-level sysctls and more
flexibility to add [custom tuning](#custom-tuning-specification)
specified by user needs.  The operator manages the containerized
[tuned](https://github.com/redhat-performance/tuned/)
daemon for [OpenShift](https://openshift.io/) as
a Kubernetes DaemonSet.  It ensures [custom tuning
specification](#custom-tuning-specification) is passed to all
containerized tuned daemons running in the cluster in the format
that the daemons understand. The daemons run on all nodes in the
cluster, one per node.

Node-level settings applied by the containerized tuned daemon are rolled back
on an event that triggers a profile change or when the containerized tuned
daemon is terminated gracefully by receiving and handling a termination signal.


## Deploying the Node Tuning Operator

The operator is deployed by applying the `*.yaml` manifests in the operator's
`/manifests` directory in alphanumeric order.  It automatically creates a default deployment
and custom resource
([CR](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/))
for the tuned daemons.  The following shows the default CR created
by the operator on a cluster with the operator installed.

```
$ oc get Tuned -n openshift-cluster-node-tuning-operator
NAME      AGE
default   1h
```

Note the default CR is meant for delivering standard node-level tuning for
the OpenShift platform and any custom changes to the the default CR will be
overwritten by the operator.  For custom tuning, create your own tuned CRs.
Newly created CRs will be combined with the default CR and custom tuning
applied to OpenShift nodes based on node/pod labels and profile priorities.

While in certain situations the support for pod labels can be a convenient
way of automatically delivering required tuning, this practice is discouraged
and strongly advised against especially in large-scale clusters.  The default
tuned CR ships without pod label matching.  If a custom profile is created
with pod label matching the functionality will be enabled at that time.


## Custom tuning specification

For an example of a tuning specification, refer to
`/assets/tuned/default-cr-tuned.yaml` in the operator's directory or to
the resource created in a live cluster by:

```
$ oc get Tuned/default -o yaml
```

The CR for the operator has two major sections.  The first
section, `profile:`, is a list of tuned profiles and their names.  The
second, `recommend:`, defines the profile selection logic.

Multiple custom tuning specifications can co-exist as multiple CRs
in the operator's namespace.  The existence of new CRs or the deletion
of old CRs is detected by the operator.  All existing custom tuning
specifications are merged and appropriate objects for the containerized
tuned daemons are updated.


### Profile data

The `profile:` section lists tuned profiles and their names.

```
  profile:
  - name: tuned_profile_1
    data: |
      # Tuned profile specification
      [main]
      summary=Description of tuned_profile_1 profile

      [sysctl]
      net.ipv4.ip_forward=1
      # ... other sysctl's or other tuned daemon plug-ins supported by the containerized tuned

  # ...

  - name: tuned_profile_n
    data: |
      # Tuned profile specification
      [main]
      summary=Description of tuned_profile_n profile

      # tuned_profile_n profile settings
```

Refer to a list of
(tuned plug-ins supported by the operator)[#supported-tuned-daemon-plug-ins].


### Recommended profiles

The `profile:` selection logic is defined by the `recommend:` section of the CR.

```
  recommend:
  - match:                              # optional; if omitted, profile match is assumed unless a profile with a higher matches first
    <match>                             # an optional array
    priority: <priority>                # profile ordering priority, lower numbers mean higher priority (0 is the highest priority)
    profile: <tuned_profile_name>       # e.g. tuned_profile_1

  # ...

  - match:
    <match>
    priority: <priority>
    profile: <tuned_profile_name>       # e.g. tuned_profile_n
```

If `<match>` is omitted, a profile match (i.e. _true_) is assumed.

`<match>` is an optional array recursively defined as follows:

```
    - label: <label_name>     # node or pod label name
      value: <label_value>    # optional node or pod label value; if omitted, the presence of <label_name> is enough to match
      type: <label_type>      # optional node or pod type ("node" or "pod"); if omitted, "node" is assumed
      <match>                 # an optional <match> array
```

If `<match>` is not omitted, all nested `<match>` sections must
also evaluate to _true_.  Otherwise, _false_ is assumed and the
profile with the respective `<match>` section will not be applied or
recommended.  Therefore, the nesting (child `<match>` sections) works as logical
_and_ operator.  Conversely, if any item of the `<match>` array matches,
the entire `<match>` array evaluates to _true_.  Therefore, the array
acts as logical _or_ operator.


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

The CR above is translated for the containerized tuned daemon into
its recommend.conf file based on the profile priorities.  The profile
with the highest priority (10) is openshift-control-plane-es and,
therefore, it is considered first.  The containerized tuned daemon
running on a given node looks to see if there is a pod running on the
same node with the tuned.openshift.io/elasticsearch label set.  If not,
the entire `<match>` section evaluates as _false_.  If there is such a
pod with the label, in order for the `<match>` section to evaluate to
_true_, the node label also needs to be node-role.kubernetes.io/master
_or_ node-role.kubernetes.io/infra.

If the labels for the profile with priority 10 matched,
openshift-control-plane-es profile is applied and no other profile is
considered.  If the node/pod label combination did not match,
the second highest priority profile (openshift-control-plane) is considered.
This profile is applied if the containerized tuned pod runs on a node with
labels node-role.kubernetes.io/master _or_ node-role.kubernetes.io/infra.

Finally, the profile `openshift-node` has the lowest priority of 30.
It lacks the `<match>` section and, therefore, will always match.  It
acts as a profile catch-all to set openshift-node profile, if no other
profile with higher priority matches on a given node.

### Example

The following CR applies custom node-level tuning for
OpenShift nodes that run an ingress pod with label
`tuned.openshift.io/ingress-pod-label=ingress-pod-label-value`.
As an administrator, use the following command to create a custom tuned CR.

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


## Supported tuned daemon plug-ins

Aside from the `[main]` section, the following
[tuned plug-ins](https://github.com/redhat-performance/tuned/tree/master/tuned/plugins)
are supported when using [custom profiles](#custom-tuning-specification) defined
in the `profile:` section of the Tuned CR:

* audio
* cpu
* disk
* eeepc_she
* modules
* mounts
* net
* scheduler
* scsi_host
* selinux
* sysctl
* sysfs
* usb
* video
* vm

with the exception of dynamic tuning functionality provided by some of the plug-ins.
The following Tuned plug-ins are currently not supported:

* bootloader
* script
* systemd
