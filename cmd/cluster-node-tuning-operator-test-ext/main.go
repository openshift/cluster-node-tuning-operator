/*
This command is used to run the Cluster OpenShift Cluster Node Tuning Operator tests extension for OpenShift.

It registers the Cluster Node Tuning Operator tests with the OpenShift Tests Extension framework and provides a command-line interface to execute them.

For further information, please refer to the documentation at:
https://github.com/openshift-eng/openshift-tests-extension/blob/main/cmd/example-tests/main.go
*/
package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/spf13/cobra"

	// The import below is necessary to ensure that the NTO operator tests are registered with the extension.
	_ "github.com/openshift/cluster-node-tuning-operator/test/extended/specs"
)

//go:embed fixture_catalog.txt
var ntoFixtureCatalog string

func init() {
	_ = ntoFixtureCatalog
}

func main() {
	registry := e.NewRegistry()
	ext := e.NewExtension("openshift", "payload", "cluster-node-tuning-operator")

	// Suite: conformance/parallel (fast, parallel-safe)
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/conformance/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`(labels.exists(l, l=="ReleaseGate")) &&
			!(name.contains("[Serial]") || name.contains("[Slow]"))`,
		},
	})

	// Suite: conformance/serial (explicitly serial tests)
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/conformance/serial",
		Parents: []string{"openshift/conformance/serial"},
		Qualifiers: []string{
			`(labels.exists(l, l=="ReleaseGate")) &&
			name.contains("[Serial]")`,
			// refer to https://github.com/openshift/origin/blob/main/pkg/testsuites/standard_suites.go
		},
	})

	// Suite: disruptive
	// See: https://github.com/openshift/release/blob/508c08ff3d8dbff48604b94484a0ce63983710ba/ci-operator/config/openshift/release/openshift-release-main__nightly-4.22.yaml#L2462
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/disruptive",
		Parents: []string{"openshift/disruptive-longrunning"},
		Qualifiers: []string{
			`(labels.exists(l, l=="ReleaseGate")) &&
			name.contains("[Disruptive]")`,
		},
	})

	// Suite: optional/slow (long-running tests)
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/optional/slow",
		Parents: []string{"openshift/optional/slow"},
		Qualifiers: []string{
			`(labels.exists(l, l=="ReleaseGate")) &&
			name.contains("[Slow]")`,
		},
	})

	// Suite: all (includes everything)
	ext.AddSuite(e.Suite{
		Name: "openshift/cluster-node-tuning-operator/all",
		Qualifiers: []string{
			`(labels.exists(l, l=="ReleaseGate"))`,
		},
	})

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	// Ensure [Disruptive] tests are also [Serial]
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		if strings.Contains(spec.Name, "[Disruptive]") && !strings.Contains(spec.Name, "[Serial]") {
			spec.Name = strings.ReplaceAll(
				spec.Name,
				"[Disruptive]",
				"[Serial][Disruptive]",
			)
		}
	})

	// Preserve original-name labels for renamed tests
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if strings.HasPrefix(label, "original-name:") {
				parts := strings.SplitN(label, "original-name:", 2)
				if len(parts) > 1 {
					spec.OriginalName = parts[1]
				}
			}
		}
	})

	// Ignore obsolete tests
	ext.IgnoreObsoleteTests(
	// "[sig-node-tuning] <test name here>",
	)

	// Initialize environment before running any tests
	specs.AddBeforeAll(func() {
		// do stuff
	})

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "Cluster Node Tuning Operator Tests Extension",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
