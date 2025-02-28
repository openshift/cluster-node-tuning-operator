package poolname

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodepools"
)

// poolName can be used interchangeably for specifying MCP name or nodePool name
// depending on the platform on which the test is running.

// GetByProfile returns the poolName which is associated with the given profile
func GetByProfile(ctx context.Context, profile *performancev2.PerformanceProfile) string {
	GinkgoHelper()
	if !hypershift.IsHypershiftCluster() {
		poolName, err := mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		return poolName
	}
	np, err := nodepools.GetNodePool(ctx, testclient.ControlPlaneClient)
	Expect(err).ToNot(HaveOccurred(), "failed to get node pool affected by profile: %q", profile.Name)
	return client.ObjectKeyFromObject(np).String()
}
