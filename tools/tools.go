//go:build tools
// +build tools

// This package contains import references to packages required only for the
// build process.
// https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module
package tools

import (
	_ "github.com/kevinburke/go-bindata/go-bindata"     // To generate bindata from /assets/tuned/manifests
	_ "github.com/openshift/build-machinery-go"         // To create patched cluster profile manifests
	_ "k8s.io/code-generator"                           // To generate DeepCopy fns() for API, clientsets/listers/informers
	_ "k8s.io/code-generator/cmd/validation-gen"        // Required by hack/update-codegen.sh
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen" // To generate tuned.openshift.io CRDs
)
