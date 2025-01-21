#!/bin/bash

set -e

# Example invocation
# ~~~~~~~~~~~~~~~~~~
# ORG=quay-user FDP_REPO_BASEURL=fdp-repo-baseurl ./test-tuned-fdp.sh
#
# This script assumes a deployed OpenShift cluster and an environment 
# configured to write the locally built IMAGE to an image repository.
#
# Also note the deploy-custom-nto.sh scales down CVO replica to 0 to
# prevent it from overriding the custom NTO image.  If you wish to
# upgrade your cluster, do not use this script or revert the change
# after using it.
#
# If you are using this script on a different architecture than x86_64,
# make sure you target x86_64 architecture by "export GOARCH=amd64"
# prior to running this script.

WORKDIR=$(realpath "${0%/*}/..")
CURRENT_SCRIPT=${0##*/}
REPO_DIR=$(mktemp -d -t "${CURRENT_SCRIPT}XXXX")
FDP_REPO_BASEURL=${FDP_REPO_BASEURL:-https://brew-task-repos.engineering.redhat.com/repos/official/tuned/2.24.0/1.2.20240819gitc082797f.el9fdp/noarch/}
RHEL_REPO_COMPOSE=${RHEL_REPO_COMPOSE:-http://download-01.eng.brq.redhat.com/rhel-9/nightly/RHEL-9/latest-RHEL-9.4/compose/}

trap cleanup INT TERM EXIT

cleanup()
{
  test -d "$REPO_DIR" && rm -rf "$REPO_DIR"
}

prepare_repo_files() {
  cat >$REPO_DIR/tuned-fdp.repo<<EOF
[tuned-fdp]
name=TuneD FDP repository
enabled=1
gpgcheck=0
baseurl=$FDP_REPO_BASEURL
module_hotfixes=1
sslverify=0
EOF

  # An alternative approach would be to use subscription-manager with either real or testing subscription server.
  # However, this script is meant for internal use only and using subscription-manager would make things more complicated.
  cat >$REPO_DIR/rhel-baseos-appstream.repo<<EOF
[rhel-9-baseos]
name=rhel-9-baseos
baseurl=$RHEL_REPO_COMPOSE/BaseOS/\$basearch/os/
enabled=1
gpgcheck=0

[rhel-9-appstream]
name=rhel-9-appstream
baseurl=$RHEL_REPO_COMPOSE/AppStream/\$basearch/os/
enabled=1
gpgcheck=0
EOF
}

deploy_custom_nto() {
  # Prepare Makefile and deploy-custom-nto.sh variables
  export IMAGE_BUILD_EXTRA_OPTS="-v=$REPO_DIR:/etc/yum.repos.d:z --env=ART_DNF_WRAPPER_POLICY=append"
  export DOCKERFILE=Dockerfile.rhel9	# Do not build TuneD from source, use pre-built TuneD RPMs.  These RPMs might contain extra patches which do not ship ustream.
  export ORG

  $WORKDIR/hack/deploy-custom-nto.sh
}

nto_run_tuned_tests() {
  # test-e2e usually take 30min
  echo "Starting test-e2e"
  make -C $WORKDIR test-e2e

  CLUSTER=mcp-only make -C $WORKDIR cluster-deploy-pao
  # e2e-gcp-pao	usually take 40min
  echo "Starting pao-functests"
  make -C $WORKDIR pao-functests

  # e2e-gcp-pao-updating-profile usually take 4-5h
  # echo "Starting e2e-gcp-pao-updating-profile"
  # make -C $WORKDIR pao-functests-updating-profile
}

prepare_repo_files
deploy_custom_nto
nto_run_tuned_tests
