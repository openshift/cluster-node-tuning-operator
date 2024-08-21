#!/bin/bash

set -e
WORKDIR=$(dirname "$(realpath "$0")")/..

# Example invocation
# ~~~~~~~~~~~~~~~~~~
# ORG=quay-user hack/deploy-custom-nto.sh
#
# This script assumes a deployed OpenShift cluster and an environment 
# configured to write the locally built IMAGE to an image repository.
#
# Also note cvo_scale_down() scales down CVO replica to 0 to prevent it from
# overriding the custom NTO image.  If you wish to upgrade your cluster,
# do not use this script.

ORG=${ORG:-openshift}	# At a minimum, you'll probably want to override this variable.
TAG=${TAG:-$(git rev-parse --abbrev-ref HEAD)}  # You may need to override this if your git branch contains special characters
IMAGE=quay.io/${ORG}/origin-cluster-node-tuning-operator:$TAG

nto_prepare_image() {
  make -C $WORKDIR update-tuned-submodule TUNED_COMMIT=${TUNED_COMMIT:-HEAD}
  make -C $WORKDIR local-image IMAGE=$IMAGE
  make -C $WORKDIR local-image-push IMAGE=$IMAGE
}

# Scale CVO down not to overwrite custom NTO.
cvo_scale() {
  local replicas=${1:-0}
  oc scale deploy/cluster-version-operator --replicas=$replicas -n openshift-cluster-version
}

nto_deploy_custom() {
  oc project openshift-cluster-node-tuning-operator
  for file in ${WORKDIR}/manifests/*.yaml; do
    # We will skip the ibm specific deployment. 
    if [[ "$file" != ${WORKDIR}/manifests/50-operator-ibm* ]]; then
      oc apply -f "$file"
    fi
  done
  oc patch deploy cluster-node-tuning-operator -p '{"spec":{"template":{"spec":{"containers":[{"name":"cluster-node-tuning-operator","image":"'$IMAGE'","env":[{"name":"CLUSTER_NODE_TUNED_IMAGE","value":"'$IMAGE'"}]}]}}}}'
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

nto_prepare_image
cvo_scale 0
nto_deploy_custom
wait_for_updated_tuned_pods
