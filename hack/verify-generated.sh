#!/bin/bash

if [[ -n "$(git status --porcelain .)" ]]; then
        echo "uncommitted generated files. run 'make generate' and commit results."
        echo "$(git status --porcelain .)"
        exit 1
fi
