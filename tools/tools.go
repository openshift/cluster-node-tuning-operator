// +build tools

// This package contains import references to packages required only for the
// build process.
// https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module
package tools

import (
	_ "github.com/kevinburke/go-bindata/go-bindata"
	_ "k8s.io/code-generator"

	// required by hack/codegen/update-crd.sh
	_ "github.com/openshift/crd-schema-gen/cmd/crd-schema-gen"
)
