#!/bin/bash

set -e

export GOROOT=$(go env GOROOT)

export PERF_PROFILE_TYPES=pkg/apis/performanceprofile/v2/performanceprofile_types.go
export PERF_PROFILE_DOC=docs/performanceprofile/performance_profile.md

_output/docs-generator -- $PERF_PROFILE_TYPES > $PERF_PROFILE_DOC

echo "API docs updated"
