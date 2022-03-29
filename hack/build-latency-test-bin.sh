#!/bin/bash

set -eu

if ! which go &>/dev/null; then
  echo "No go command available"
  exit 1
fi

go test -v -c -o build/_output/bin/latency-e2e.test ./functests/4_latency
