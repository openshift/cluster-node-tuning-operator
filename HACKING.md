⚠⚠⚠ THIS IS A LIVING DOCUMENT AND LIKELY TO CHANGE QUICKLY ⚠⚠⚠

# Hacking on the Node Tuning Operator

These instructions have been tested on Fedora 36.

## Prerequisites

Most changes you'll want to test live against a real cluster. Instructions are TODO.

Go v1.18 is used for development of the NTO.

# Static Analysis

Static analysis (e.g. gofmt) can be executed with:

`make verify`

# Unit Tests

Unit tests (that don't interact with a running cluster) can be executed with:

`make test-unit`

# Local e2e tests

Local end-to-end tests (that don't interact with a running cluster) can be executed with:

`make test-e2e-local`

Note: This will install ginkgo if it is not already installed. You must ensure that the
go bin directory (usually $HOME/go/bin) is in your $PATH.

# Running in a cluster

During NTO development there are certain test scenarios
that require a cluster to properly test your code.

In those situations, you will need to:

1) build the NTO image you're interested in testing
2) Push the image to an OpenShift cluster

## Build NTO image

To build an image that contains all NTO components run:

```
# Specify the registry
REGISTRY=quay.io
# Specify the registry user
ORG=username
# Specify the tag
TAG=tag

make local-image IMAGE=${REGISTRY}/${ORG}/cluster-node-tuning-operator:${TAG}
```

After the build is complete, you can push the image to a registry with:

```
# Specify the registry
REGISTRY=quay.io
# Specify the registry user
ORG=username
# Specify the tag
TAG=tag

make local-image-push IMAGE=${REGISTRY}/${ORG}/cluster-node-tuning-operator:${TAG}
```

## Push the image to an OpenShift cluster

```
override_tuning_operator ()
{
  oc patch clusterversion version --type json -p '[{"op":"add","path":"/spec/overrides","value":[{"kind":"Deployment","group":"apps","name":"cluster-node-tuning-operator","namespace":"openshift-cluster-node-tuning-operator","unmanaged":true}]}]'
}

custom_tuning_operator ()
{
  TUNING_IMAGE=${REGISTRY}/${ORG}/cluster-node-tuning-operator:${TAG}
  override_tuning_operator
  oc scale deployment -n openshift-cluster-node-tuning-operator cluster-node-tuning-operator --replicas=0
  oc patch deployment -n openshift-cluster-node-tuning-operator cluster-node-tuning-operator -p \
    '{"spec":{"template":{"spec":{"containers":[{"name":"cluster-node-tuning-operator","image":"'${TUNING_IMAGE}'"}]}}}}'
  oc patch deployment -n openshift-cluster-node-tuning-operator cluster-node-tuning-operator -p \
    '{"spec":{"template":{"spec":{"containers":[{"name":"cluster-node-tuning-operator","env":[{"name":"CLUSTER_NODE_TUNED_IMAGE","value":"'${TUNING_IMAGE}'"}]}]}}}}'
  oc describe  deployment -n openshift-cluster-node-tuning-operator cluster-node-tuning-operator | grep --color=auto Image
  oc describe  deployment -n openshift-cluster-node-tuning-operator cluster-node-tuning-operator | grep --color=auto CLUSTER_NODE_TUNED_IMAGE
  oc delete ds -n openshift-cluster-node-tuning-operator tuned
  oc scale deployment -n openshift-cluster-node-tuning-operator cluster-node-tuning-operator --replicas=1
  sleep 10
  oc wait deployment -n openshift-cluster-node-tuning-operator cluster-node-tuning-operator --for condition=Available=True --timeout=90s
  for i in {1..36}; do
    echo "Waiting for DS to be deployed, try $i"
    sleep 10
    if oc get ds -n openshift-cluster-node-tuning-operator tuned 2>/dev/null; then
      break
    fi
  done
  oc rollout status daemonset -n openshift-cluster-node-tuning-operator tuned --timeout=90s
  oc get deployment -n openshift-cluster-node-tuning-operator
  oc get ds -n openshift-cluster-node-tuning-operator
}
```
