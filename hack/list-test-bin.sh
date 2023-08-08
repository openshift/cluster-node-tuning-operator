#!/bin/bash

set -e

SUITEPATH="./test/e2e/performanceprofile/functests"

if [ -z "$1" ]; then
	ls $SUITEPATH | grep -E '[0-9]+_.*'
	exit 0
fi

if [ ! -d "$SUITEPATH/$1" ]; then
	echo "unknown suite: $1"
	exit 1
fi

exit 0
