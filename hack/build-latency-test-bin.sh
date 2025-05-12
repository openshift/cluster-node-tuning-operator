#!/bin/bash

set -eu

if ! which go &>/dev/null; then
  echo "No go command available"
  exit 1
fi

CGO_ENABLED=1 go test -v -c -o build/_output/bin/latency-e2e.test ./test/e2e/performanceprofile/functests/4_latency
