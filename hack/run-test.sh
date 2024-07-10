#!/bin/bash

readonly CURRENT_SCRIPT=$(basename $0)

HEADER_MESSAGE="Running Tests "

usage() {
    print "Usage:"
    print "  ${CURRENT_SCRIPT} [-h] [-c] [-d] -s suites -p extraparams [-m header_message]"
    print ""
    print "Options:"
    print "    -h                Help for ${CURRENT_SCRIPT}"
    print "    -t                list of space separated paths to Testsuites to execute"
    print "    -p                string with extra Params for ginkgo"
    print "    -r                string with report Params for ginkgo (these params will go after the list of suites)"
    print "    -m                Message header to print before test execution. Default '${HEADER_MESSAGE}'"
    print "    -d                Ginkgo dry-run. Will output all ginkgo test without execute them, letting you know the actual tests to be executed with the given parameters"
    print "    -c                Command line print. This will just print ginkgo command line to be execting without actually executing it."
}

exit_error() {
    echo "" >&2
    echo "error: $*" >&2
    exit 1
}

print() {
    echo "$*" >&1
}

HEADER_MESSAGE="Running Tests "
ONLY_CLI_PRINT=false
DRY_RUN=""

main() {
    while getopts ':h:c:dt:p:m:r:' OPT; do
        case "$OPT" in
        h)
            usage
            exit 0
            ;;
        t)
            GINKGO_SUITS="${OPTARG}"
            ;;
        p)
            EXTRA_PARAMS="${OPTARG}"
            ;;
        m)
            HEADER_MESSAGE="${OPTARG}"
            ;;
        r)
            REPORT_PARAMS="${OPTARG}"
            ;;
        d)
            DRY_RUN="--dry-run"
            ;;
        c)
            ONLY_CLI_PRINT=true
            ;;
        ?)
            usage
            exit_error "invalid argument: ${OPTARG}"
            ;;
        esac
    done
    shift $((OPTIND - 1))

    if [ -z "$GINKGO_SUITS" ] || [ -z "$EXTRA_PARAMS" ]; then
        usage
        exit_error "Missing arguments -t or -p"
    fi

    which ginkgo
    if [ $? -ne 0 ]; then
        echo "Downloading ginkgo tool"
        GINKGO_VERSION=$(go list -m -f '{{.Version}}' github.com/onsi/ginkgo/v2)
        go install -mod=mod github.com/onsi/ginkgo/v2/ginkgo@"${GINKGO_VERSION}"
        ginkgo version
    fi

    NO_COLOR=""
    if ! which tput &>/dev/null 2>&1 || [[ $(tput -T$TERM colors) -lt 8 ]]; then
        echo "Terminal does not seem to support colored output, disabling it"
        NO_COLOR="--no-color"
    fi

    MESSAGE="${HEADER_MESSAGE}: ${GINKGO_SUITS}"
    print ${MESSAGE}

    GINKGO_FLAGS="${NO_COLOR} ${DRY_RUN} ${EXTRA_PARAMS} --require-suite ${GINKGO_SUITS} ${REPORT_PARAMS}"
    print "Command to run: GOFLAGS=-mod=vendor ginkgo ${GINKGO_FLAGS}"

    if [ "$ONLY_CLI_PRINT" = true ]; then
        print "Skipping execution as requested by user ... "
    else
        print "Executing test suites ... "
        GOFLAGS=-mod=vendor ginkgo ${GINKGO_FLAGS}
    fi
}

main "$@"
