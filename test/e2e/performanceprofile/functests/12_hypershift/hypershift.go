package __hypershift

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	manifestsutil "github.com/openshift/cluster-node-tuning-operator/pkg/util"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodepools"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

var _ = Describe("Multiple performance profile in hypershift", Label("Hypershift"), Ordered, func() {
	var profile, secondProfile *performancev2.PerformanceProfile
	var nodePools []*hypershiftv1beta1.NodePool
	var np *hypershiftv1beta1.NodePool
	var workerRTNodes []corev1.Node
	var err error

	chkKubeletConfig := []string{"cat", "/rootfs/etc/kubernetes/kubelet.conf"}
	chkKubeletConfigFn := func(ctx context.Context, node *corev1.Node) (string, error) {
		out, err := nodes.ExecCommand(ctx, node, chkKubeletConfig)
		if err != nil {
			return "", err
		}
		output := testutils.ToString(out)
		return output, nil
	}

	BeforeAll(func() {
		By("Checking if discovery mode is enabled and performance profile is not found")
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		nodeLabel := testutils.NodeSelectorLabels
		testlog.Infof("Node label: %v", nodeLabel)
		workerRTNodes, err = nodes.GetByLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		profile, err = profiles.GetByNodeLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		hostedClusterName, err := hypershift.GetHostedClusterName()
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("Hosted Cluster: %q", hostedClusterName)
		nodePools, err = ListNodePools(context.TODO(), testclient.ControlPlaneClient, hostedClusterName)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodePools).ToNot(BeEmpty(), "no node pools found")
		if len(nodePools) < 2 {
			Skip("Skipping the test as the number of node pools is less than 2")
		}
		PrintNodePoolProfiles(nodePools)

		By("Creating a deep copy of the performance profile for the second node pool")
		secondProfile = profile.DeepCopy()
		secondProfile.Name = "second-profile"
		np = nodePools[1]
		Expect(len(np.Spec.TuningConfig)).To(BeZero(), "Expected no profile attached to nodepool, but found %d profiles", len(np.Spec.TuningConfig))
		By("Creating the second performance profile in the control plane")
		Expect(testclient.ControlPlaneClient.Create(context.TODO(), secondProfile)).To(Succeed(), "Failed to create the performance profile")

		By("Attaching the tuning object to the second node pool")
		Expect(nodepools.AttachTuningObjectToNodePool(context.TODO(), testclient.ControlPlaneClient, secondProfile, nodePools[1])).To(Succeed())

		By("Waiting for the second node pool configuration to start updating")
		err = nodepools.WaitForUpdatingConfig(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for the second node pool configuration to be ready")
		err = nodepools.WaitForConfigToBeReady(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Multiple Nodepool", Ordered, func() {
		isolated := performancev2.CPUSet("1-2")
		reserved := performancev2.CPUSet("0,3")
		policy := "best-effort"

		It("[test_id:75077] should verify support for different performance profiles on hosted cluster via multiple node pools", func() {
			By("Checking if reserved CPUs are correctly set on the worker nodes")
			for _, node := range workerRTNodes {
				By(fmt.Sprintf("Checking kubelet configuration for node %s", node.Name))

				result, err := chkKubeletConfigFn(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred(), "Failed to fetch kubelet configuration")

				obj, err := manifestsutil.DeserializeObjectFromData([]byte(result), kubeletconfigv1beta1.AddToScheme)
				Expect(err).ToNot(HaveOccurred(), "Failed to deserialize kubelet configuration")

				kc, ok := obj.(*kubeletconfigv1beta1.KubeletConfiguration)
				Expect(ok).To(BeTrue(), "Deserialized object is not of type KubeletConfiguration")

				Expect(kc.ReservedSystemCPUs).To(Equal("0"), "ReservedSystemCPUs is not correctly set")
				Expect(kc.TopologyManagerPolicy).To(Equal("single-numa-node"), "TopologyManagerPolicy is not correctly set")
			}
		})

		It("[test_id:75078] should verify that Performance Profile update re-creates only target nodepool nodes", func() {
			By("Printing node pool profiles before updating the performance profile")
			PrintNodePoolProfiles(nodePools)

			By("Modifying the second profile CPU and NUMA configurations")
			secondProfile.Spec.CPU = &performancev2.CPU{
				BalanceIsolated: ptr.To(false),
				Reserved:        &reserved,
				Isolated:        &isolated,
			}
			secondProfile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}

			By("Updating the second node pool performance profile")
			profiles.UpdateWithRetry(secondProfile)

			By("Waiting for the second node pool configuration to start updating")
			err = nodepools.WaitForUpdatingConfig(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for the second node pool configuration to be ready")
			err = nodepools.WaitForConfigToBeReady(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
			Expect(err).ToNot(HaveOccurred())

			NodePoolLabel := map[string]string{
				fmt.Sprintf("%s/%s", "hypershift.openshift.io", "nodePool"): nodePools[1].Name,
			}
			workerRTNodes, err = nodes.GetByLabels(NodePoolLabel)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the kubelet parameters were correctly updated for the worker nodes")
			for _, node := range workerRTNodes {
				By(fmt.Sprintf("Checking kubelet configuration for node %s", node.Name))

				result, err := chkKubeletConfigFn(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred(), "Failed to fetch kubelet configuration")

				obj, err := manifestsutil.DeserializeObjectFromData([]byte(result), kubeletconfigv1beta1.AddToScheme)
				Expect(err).ToNot(HaveOccurred(), "Failed to deserialize kubelet configuration")

				kc, ok := obj.(*kubeletconfigv1beta1.KubeletConfiguration)
				Expect(ok).To(BeTrue(), "Deserialized object is not of type KubeletConfiguration")

				Expect(kc.ReservedSystemCPUs).To(Equal("0,3"), "ReservedSystemCPUs is not correctly set")
				Expect(kc.TopologyManagerPolicy).To(Equal("best-effort"), "TopologyManagerPolicy is not correctly set")
			}
		})

		AfterAll(func() {
			By("Deleting the second Profile")
			Expect(nodepools.DeattachTuningObjectToNodePool(context.TODO(), testclient.ControlPlaneClient, secondProfile, nodePools[1])).To(Succeed())

			By("Waiting for the second node pool configuration to start updating after profile deletion")
			err = nodepools.WaitForUpdatingConfig(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for the second node pool configuration to be ready after profile deletion")
			err = nodepools.WaitForConfigToBeReady(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
			Expect(err).ToNot(HaveOccurred())

			PrintNodePoolProfiles(nodePools)
		})
	})
})

func ListNodePools(ctx context.Context, c client.Client, hostedClusterName string) ([]*hypershiftv1beta1.NodePool, error) {
	npList := &hypershiftv1beta1.NodePoolList{}
	if err := c.List(ctx, npList); err != nil {
		return nil, err
	}

	var nodePools []*hypershiftv1beta1.NodePool
	for i := range npList.Items {
		np := &npList.Items[i]
		if np.Spec.ClusterName == hostedClusterName {
			nodePools = append(nodePools, np)
		}
	}

	if len(nodePools) == 0 {
		return nil, fmt.Errorf("no nodePools found for hosted cluster %q", hostedClusterName)
	}

	return nodePools, nil
}

func PrintNodePoolProfiles(nodePools []*hypershiftv1beta1.NodePool) {
	for _, np := range nodePools {
		if len(np.Spec.TuningConfig) == 0 {
			testlog.Infof("NodePool %q does not have a tuningConfig profile", np.Name)
		}
		for _, tuningConfig := range np.Spec.TuningConfig {
			testlog.Infof("NodePool %q is using profile: %q", np.Name, tuningConfig.Name)
		}
	}
}
