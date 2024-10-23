package __hypershift

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
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
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

type checkFunction func(context.Context, *corev1.Node) (string, error)

var _ = Describe("Multiple performance profile in hypershift", Ordered, func() {
	var profile, secondProfile *performancev2.PerformanceProfile
	var nodePools []*hypershiftv1beta1.NodePool
	var np *hypershiftv1beta1.NodePool
	var workerRTNodes []corev1.Node
	var err error

	nodeLabel := testutils.NodeSelectorLabels
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
		workerRTNodes, err = nodes.GetByLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		profile, err = profiles.GetByNodeLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		hostedClusterName, err := hypershift.GetHostedClusterName()
		Expect(err).ToNot(HaveOccurred())
		nodePools, err = ListNodePools(context.TODO(), testclient.ControlPlaneClient, hostedClusterName)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodePools).ToNot(BeEmpty(), "no node pools found")
		Expect(len(nodePools)).To(BeNumerically(">=", 2))
		PrintNodePoolProfiles(nodePools)
	})

	Context("Multiple Nodepool", Ordered, func() {
		isolated := performancev2.CPUSet("1-2")
		reserved := performancev2.CPUSet("0,3")
		policy := "best-effort"

		It("should verify support for different performance profiles on hosted cluster via multiple node pools", func() {
			By("Creating a deep copy of the performance profile for the second node pool")
			secondProfile = profile.DeepCopy()
			secondProfile.Name = "second-profile"
			np = nodePools[1]
			By("Creating the second performance profile in the control plane")
			Expect(testclient.ControlPlaneClient.Create(context.TODO(), secondProfile)).To(Succeed(), "Failed to create the performance profile")

			By("Attaching the tuning object to the second node pool")
			Expect(nodepools.AttachTuningObject(context.TODO(), testclient.ControlPlaneClient, secondProfile, nodePools[1])).To(Succeed())

			By("Waiting for the second node pool configuration to start updating")
			err = nodepools.WaitForUpdatingConfig(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for the second node pool configuration to be ready")
			err = nodepools.WaitForConfigToBeReady(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
			Expect(err).ToNot(HaveOccurred())

			By("Checking if reserved CPUs are correctly set on the worker nodes")
			DescribeTable("Verify that kubelet parameters were updated", func(ctx context.Context, cmdFn checkFunction, getterFn func(kubeletCfg *kubeletconfigv1beta1.KubeletConfiguration) string, wantedValue string) {
				verifyKubeletParameters(ctx, workerRTNodes, chkKubeletConfigFn, getterFn, wantedValue)
			},
				Entry("verify that reservedSystemCPUs was updated", context.TODO(), chkKubeletConfigFn, func(k *kubeletconfigv1beta1.KubeletConfiguration) string { return k.ReservedSystemCPUs }, "0"),
				Entry("verify that topologyManager was updated", context.TODO(), chkKubeletConfigFn, func(k *kubeletconfigv1beta1.KubeletConfiguration) string { return k.TopologyManagerPolicy }, "single-numa-node"),
			)
		})

		It("should verify that Performance Profile update re-creates only target nodepool nodes", func() {
			By("Printing node pool profiles before updating the performance profile")
			PrintNodePoolProfiles(nodePools)

			By("Modifying the second profile CPU and NUMA configurations")
			secondProfile.Spec.CPU = &performancev2.CPU{
				BalanceIsolated: pointer.Bool(false),
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
			DescribeTable("Verify that kubelet parameters were updated", func(ctx context.Context, cmdFn checkFunction, getterFn func(kubeletCfg *kubeletconfigv1beta1.KubeletConfiguration) string, wantedValue string) {
				verifyKubeletParameters(ctx, workerRTNodes, chkKubeletConfigFn, getterFn, wantedValue)
			},
				Entry("verify that reservedSystemCPUs was updated", context.TODO(), chkKubeletConfigFn, func(k *kubeletconfigv1beta1.KubeletConfiguration) string { return k.ReservedSystemCPUs }, "0"),
				Entry("verify that topologyManager was updated", context.TODO(), chkKubeletConfigFn, func(k *kubeletconfigv1beta1.KubeletConfiguration) string { return k.TopologyManagerPolicy }, "single-numa-node"),
			)
		})

		AfterAll(func() {
			By("Deleting the second Profile")
			Expect(nodepools.DeattachTuningObject(context.TODO(), testclient.ControlPlaneClient, secondProfile, nodePools[1])).To(Succeed())

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
		for _, tuningConfig := range np.Spec.TuningConfig {
			testlog.Infof("NodePool %q is using profile: %q", np.Name, tuningConfig.Name)
		}
		if len(np.Spec.TuningConfig) == 0 {
			testlog.Infof("NodePool %q does not have a tuningConfig profile", np.Name)
		}
	}
}

func verifyKubeletParameters(ctx context.Context, workerRTNodes []corev1.Node, cmdFn checkFunction, getterFn func(kubeletCfg *kubeletconfigv1beta1.KubeletConfiguration) string, wantedValue string) {
	for _, node := range workerRTNodes {
		result, _ := cmdFn(ctx, &node)
		obj, err := manifestsutil.DeserializeObjectFromData([]byte(result), kubeletconfigv1beta1.AddToScheme)
		Expect(err).ToNot(HaveOccurred())
		kc, ok := obj.(*kubeletconfigv1beta1.KubeletConfiguration)
		Expect(ok).To(BeTrue(), "wrong type %T", obj)
		Expect(getterFn(kc)).To(Equal(wantedValue))
	}
}
