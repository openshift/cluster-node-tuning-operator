#!/usr/bin/env bash

set -e

OUTDIR="build/_output/coverage"
mkdir -p "$OUTDIR"

COVER_FILE="${OUTDIR}/cover.out"
FUNC_FILE="${OUTDIR}/coverage.txt"
HTML_FILE="${OUTDIR}/coverage.html"

echo "running unittests with coverage"
GOFLAGS=-mod=vendor go test -race -covermode=atomic -coverprofile="${COVER_FILE}" -v ./pkg/... ./controllers/... ./api/...

if [[ -n "${DRONE}" ]]; then

  # Uploading coverage report to coveralls.io
  go get github.com/mattn/goveralls

  # we should update the vendor/modules.txt once we got a new package
  go mod vendor
  $(go env GOPATH)/bin/goveralls -coverprofile="$COVER_FILE" -service=drone.io

else

  echo "creating coverage reports"
  go tool cover -func="${COVER_FILE}" > "${FUNC_FILE}"
  go tool cover -html="${COVER_FILE}" -o "${HTML_FILE}"
  echo "find coverage reports at ${OUTDIR}"

fi
