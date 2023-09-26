#!/bin/bash

GINKGO_SUITS=${GINKGO_SUITS:-"./test/e2e/performanceprofile/functests/0_config ./test/e2e/performanceprofile/functests/5_latency_testing"}

which ginkgo
if [ $? -ne 0 ]; then
	echo "Downloading ginkgo tool"
	go install github.com/onsi/ginkgo/ginkgo
fi

NO_COLOR=""
if ! which tput &> /dev/null 2>&1 || [[ $(tput -T$TERM colors) -lt 8 ]]; then
  echo "Terminal does not seem to support colored output, disabling it"
  NO_COLOR="-noColor"
fi


echo "Running Functional Tests: ${GINKGO_SUITS}"
# -v: print out the text and location for each spec before running it and flush output to stdout in realtime
# -r: run suites recursively
# --fail-fast: ginkgo will stop the suite right after the first spec failure
# --flake-attempts: rerun the test if it fails
# --require-suite: fail if tests are not executed because of missing suite
GOFLAGS=-mod=vendor ginkgo $NO_COLOR --v -r --fail-fast --flake-attempts=2 --timeout=5h --require-suite ${GINKGO_SUITS} --junit-report=/tmp/artifacts
