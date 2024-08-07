#!/bin/bash

set -e

PREFIX="build-e2e-"
SUITEPATH="./test/e2e"
TARGET=$1

if [ -z "$TARGET" ]; then
	echo "usage: $0 suite"
	echo "example: $0 deferred"
	exit 1
fi

OUTDIR="${2:-_output}"

if [ ! -d "$SUITEPATH/$TARGET" ]; then
	echo "unknown suite: $TARGET"
	echo -e "must be one of:\n$( ls $SUITEPATH | grep -E '[0-9]+_.*' )"
	exit 2
fi

SUITE="${SUITEPATH}/${TARGET}"
SUFFIX=$( echo $TARGET | cut -d_ -f2- )
BASENAME="e2e"
EXTENSION="test"
OUTPUT="${BASENAME}-${SUFFIX}.${EXTENSION}"

echo "${SUITE} -> ${OUTDIR}/${OUTPUT}"
go test -c -v -o ${OUTDIR}/${OUTPUT} ${SUITE}
