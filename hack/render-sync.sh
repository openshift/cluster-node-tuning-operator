#!/usr/bin/env bash
WORKDIR=$(dirname "$(realpath "$0")")/..

function join_by { local IFS="$1"; shift; echo "$*"; }

function rendersync() {
  INPUT_DIRS=()
  EXTRA_ARGS=()
  if [[ "$1" =~ ^-.* ]]; then
      while [[ "x$1" != "x--" ]]; do
          EXTRA_ARGS+=("$1")
          shift
      done
      shift
  fi
  while (( $# > 1 )); do
    INPUT_DIRS+=("${WORKDIR}/test/e2e/performanceprofile/cluster-setup/$1")
    shift
  done
  INPUT_DIRS=$(join_by , ${INPUT_DIRS[@]})

  OUTPUT_DIR=$1

  echo "== Rendering ${INPUT_DIRS} into ${OUTPUT_DIR}"

  ARTIFACT_DIR=$(mktemp -d)

  cd "${WORKDIR}" || { echo "failed to change dir to ${WORKDIR}"; exit; }
  _output/cluster-node-tuning-operator render ${EXTRA_ARGS[@]} \
  --asset-input-dir "${INPUT_DIRS}" \
  --asset-output-dir "${ARTIFACT_DIR}"

  cp "${ARTIFACT_DIR}"/*  "${WORKDIR}"/test/e2e/performanceprofile/testdata/render-expected-output/${OUTPUT_DIR}
  for f in  "${WORKDIR}"/test/e2e/performanceprofile/testdata/render-expected-output/${OUTPUT_DIR}/*
  do
    sed -i "s/uid:.*/uid: \"\"/" "${f}"
  done
  rm -r "${ARTIFACT_DIR}"
}

rendersync manual-cluster/performance base/performance default
rendersync bootstrap-cluster/performance pinned-cluster/default bootstrap/no-mcp
rendersync bootstrap-cluster/performance pinned-cluster/default bootstrap-cluster/extra-mcp bootstrap/extra-mcp
rendersync bootstrap-cluster/performance pinned-cluster/default container-runtime-crun bootstrap/extra-ctrcfg
rendersync --owner-ref none -- base/performance manual-cluster/performance no-ref 