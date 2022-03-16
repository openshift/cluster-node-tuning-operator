#!/bin/bash

# This is generating a release note, which will be used for github releases

RELEASE_NOTE_FILE="build/_output/release-note.md"

# Current tag
RELREF=${RELREF:-$(git describe --abbrev=0 --tags)}

# Previous tag
PREREF=${PREREF:-$(git describe --abbrev=0 --tags $RELREF^)}

RELSPANREF=$PREREF..$RELREF

GHRELURL="https://github.com/openshift/cluster-node-tuning-operator/releases/tag/"
RELURL="$GHRELURL$RELREF"

CHANGES_COUNT=$(git log --oneline $RELSPANREF | wc -l)
CHANGES_BY_COUNT=$(git shortlog -sne $RELSPANREF | wc -l)
STATS=$(git diff --shortstat $RELSPANREF)

cat <<EOF > "${RELEASE_NOTE_FILE}"
## Performance Addon Operator

This is release "${RELREF}" of the performance addon operator, which follows "${PREREF}".
This release consists of ${CHANGES_COUNT} changes by ${CHANGES_BY_COUNT} contributers:
${STATS}

The primary release artifact of the performance addon operator is the git tree.
The source code and selected build artifacts are available for download at:
${RELURL}

Pre-built containers are published on quay.io and can be viewed at:
https://quay.io/organization/openshift-kni

### Notable changes

*TODO*

EOF