#!/usr/bin/env bash

WORKDIR=$(dirname "$(realpath "$0")")/..
ARTIFACT_DIR=$(mktemp -d)

cd "${WORKDIR}" || { echo "failed to change dir to ${WORKDIR}"; exit; }
_output/cluster-node-tuning-operator render \
--performance-profile-input-files "${WORKDIR}"/test/e2e/performanceprofile/cluster-setup/manual-cluster/performance/performance_profile.yaml \
--asset-input-dir "${WORKDIR}"/build/assets \
--asset-output-dir "${ARTIFACT_DIR}"

cp -r "${ARTIFACT_DIR}"/*  "${WORKDIR}"/test/e2e/performanceprofile/testdata/render-expected-output/
rm -r "${ARTIFACT_DIR}"
