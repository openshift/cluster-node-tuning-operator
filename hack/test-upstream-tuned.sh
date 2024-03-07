#!/bin/bash

set -e

# Example invocation
# ~~~~~~~~~~~~~~~~~~
# ORG=quay-user hack/test-upstream-tuned.sh
#
# This script assumes a deployed OpenShift cluster and an environment 
# configured to write the locally built IMAGE to an image repository.
#
# Also note cvo_scale_down() scales down CVO replica to 0 to prevent it from
# overriding the custom NTO image.  If you wish to upgrade your cluster,
# do not use this script.

ORG=${ORG:-openshift}	# At a minimum, you'll probably want to override this variable.
TAG=$(git rev-parse --abbrev-ref HEAD)
IMAGE=quay.io/${ORG}/origin-cluster-node-tuning-operator:$TAG

nto_prepare_image() {
  make clone-tuned TUNED_COMMIT=${TUNED_COMMIT:-HEAD}
  make local-image IMAGE=$IMAGE
  make local-image-push IMAGE=$IMAGE
}

# Scale CVO down not to overwrite custom NTO.
cvo_scale() {
  local replicas=${1:-0}
  oc scale deploy/cluster-version-operator --replicas=$replicas -n openshift-cluster-version
}

nto_deploy_custom() {
  oc project openshift-cluster-node-tuning-operator
  oc patch deploy cluster-node-tuning-operator -p '{"spec":{"template":{"spec":{"containers":[{"env":[{"name":"CLUSTER_NODE_TUNED_IMAGE","value":"'$IMAGE'"}],"name":"cluster-node-tuning-operator"}]}}}}'
}

wait_for_updated_tuned_pods() {
  local image ready updated p

  while ! test "$updated"
  do
    test "$p" && {
      echo "Waiting for TuneD pods with $IMAGE image and ready." >&2
      sleep 5
    }
    for p in $(oc get po -l openshift-app=tuned -o name)
    do
      updated=
      image=$(oc get $p -o jsonpath='{.spec.containers[0].image}')
      test "$image" = "$IMAGE" && {
        ready=$(oc get $p -o jsonpath='{.status.containerStatuses[0].ready}')
        test "$ready" = true || break
      } || break
      updated=Y
    done
    updated=$updated
  done
}

nto_run_tuned_tests() {
  # test-e2e usually take 30min
  make test-e2e

  CLUSTER=mcp-only make cluster-deploy-pao
  # e2e-gcp-pao	usually take 40min
  make pao-functests

  # e2e-gcp-pao-updating-profile usually take 4-5h
  make pao-functests-updating-profile
}

nto_prepare_image
cvo_scale 0
nto_deploy_custom
wait_for_updated_tuned_pods
nto_run_tuned_tests
cvo_scale 1
