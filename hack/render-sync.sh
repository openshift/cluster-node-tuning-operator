#!/usr/bin/env bash

WORKDIR=$(dirname "$(realpath "$0")")/..
ARTIFACT_DIR=$(mktemp -d)

cd "${WORKDIR}" || { echo "failed to change dir to ${WORKDIR}"; exit; }
_output/cluster-node-tuning-operator render \
--asset-input-dir "${WORKDIR}"/test/e2e/performanceprofile/cluster-setup/manual-cluster/performance,"${WORKDIR}"/test/e2e/performanceprofile/cluster-setup/base/performance \
--asset-output-dir "${ARTIFACT_DIR}"

cp "${ARTIFACT_DIR}"/*  "${WORKDIR}"/test/e2e/performanceprofile/testdata/render-expected-output/default
for f in  "${WORKDIR}"/test/e2e/performanceprofile/testdata/render-expected-output/default/*
do
  sed -i "s/uid:.*/uid: \"\"/" "${f}"
done
rm -r "${ARTIFACT_DIR}"
