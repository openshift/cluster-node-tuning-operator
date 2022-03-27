#!/bin/bash

set -e

export GOROOT=$(go env GOROOT)

export PERF_PROFILE_TYPES=api/v2/performanceprofile_types.go
export PERF_PROFILE_DOC=docs/performance_profile.md

# using the generated CSV, create the real CSV by injecting all the right data into it
build/_output/bin/docs-generator -- $PERF_PROFILE_TYPES > $PERF_PROFILE_DOC

echo "API docs updated"
