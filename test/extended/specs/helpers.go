// Package-level helpers that depend on the Ginkgo/Gomega test framework.
//
// Convention: the test/extended/utils package is intentionally Ginkgo- and
// Gomega-free so its helpers stay framework-agnostic and unit-testable outside
// Ginkgo. Any helper that needs test-control flow (e.g. g.Skip) or Gomega
// assertions (e.g. o.Eventually) belongs HERE, in the specs package, rather
// than in test/extended/utils. These wrappers are thin shims over the pure
// predicates/helpers provided by utils (e.g. utils.IsSNOCluster).
package specs

import (
	"fmt"

	g "github.com/onsi/ginkgo/v2"

	utils "github.com/openshift/cluster-node-tuning-operator/test/extended/utils"
)

// SkipNoNTO skips the owning test when the NTO operator is not installed.
// It lives in the specs package (not utils) so that utils stays Ginkgo/Gomega-free.
func SkipNoNTO(oc *utils.CLI, namespace string) {
	installed, err := utils.IsNTOInstalled(oc, namespace)
	if err != nil {
		g.Fail(fmt.Sprintf("failed to check if NTO is installed: %v", err))
	}
	if !installed {
		g.Skip("NTO is not installed - skipping test")
	}
}

// SkipIsSNO skips the owning test on Single Node OpenShift clusters.
func SkipIsSNO(oc *utils.CLI) {
	if utils.IsSNOCluster(oc) {
		g.Skip("Single Node Cluster - skipping test")
	}
}

// SkipIsHyperShiftHostedCluster skips the owning test on HyperShift hosted clusters.
func SkipIsHyperShiftHostedCluster(oc *utils.CLI) {
	if utils.IsHyperShiftHostedCluster(oc) {
		g.Skip("HyperShift Hosted Cluster - skipping test")
	}
}
