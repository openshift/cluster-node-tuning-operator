#!/bin/bash

set -x

GINKGO_SUITS=${GINKGO_SUITS:-"test/e2e/pao/functests"}
LATENCY_TEST_RUN=${LATENCY_TEST_RUN:-"false"}

NO_COLOR=""
if ! which tput &> /dev/null 2>&1 || [[ $(tput -T$TERM colors) -lt 8 ]]; then
  echo "Terminal does not seem to support colored output, disabling it"
  NO_COLOR="--ginkgo.noColor"
fi

# run the latency tests under the OpenShift CI, just to verify that the image works
if [ -n "${IMAGE_FORMAT}" ]; then
  LATENCY_TEST_RUN="true"
fi


echo "Running Functional Tests: ${GINKGO_SUITS}"
# -v: print out the text and location for each spec before running it and flush output to stdout in realtime
# -r: run suites recursively
# --failFast: ginkgo will stop the suite right after the first spec failure
# --flakeAttempts: rerun the test if it fails
# -requireSuite: fail if tests are not executed because of missing suite
go test ./${GINKGO_SUITS} $NO_COLOR -gingko.v -ginkgo.failFast -ginkgo.flakeAttempts=2
