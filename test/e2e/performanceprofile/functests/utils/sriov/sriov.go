package sriov

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovk8sv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	sriovcluster "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	sriovnetwork "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// OperatorNamespace is the namespace where the SR-IOV Operator is installed
	OperatorNamespace = "openshift-sriov-network-operator"
)

func CreatePolicyAndNetwork(ctx context.Context, sriovInfos *sriovcluster.EnabledNodes, namespace, networkName, resourceName, metaPluginsConfig string) {
	GinkgoHelper()
	numVfs := 4
	ppAndSRIOVNodes, err := getNodesWithPerformanceProfileAndSRIOV(sriovInfos.Nodes)
	Expect(err).ToNot(HaveOccurred())
	Expect(len(ppAndSRIOVNodes)).To(Not(BeZero()))

	node := ppAndSRIOVNodes[0]
	sriovDevice, err := sriovInfos.FindOneSriovDevice(node)
	Expect(err).ToNot(HaveOccurred())

	testlog.Infof("creating SRI-OV devices for node=%q", node)
	_, err = sriovnetwork.CreateSriovPolicy(testclient.SRIOVClient, "test-policy", OperatorNamespace, sriovDevice.Name, node, numVfs, resourceName, "netdevice")
	Expect(err).ToNot(HaveOccurred())
	waitStable()

	Eventually(func() int64 {
		testedNode, err := testclient.K8sClient.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		resNum, _ := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
		capacity, _ := resNum.AsInt64()
		return capacity
	}, 10*time.Minute, time.Second).Should(Equal(int64(numVfs)))

	createNetwork(sriovDevice, networkName, namespace, OperatorNamespace, resourceName, metaPluginsConfig)
}

func createNetwork(sriovDevice *sriovv1.InterfaceExt, sriovNetworkName, sriovNetworkNamespace, operatorNamespace, resourceName, metaPluginsConfig string) {
	createNetworkWithVlan(sriovDevice, sriovNetworkName, sriovNetworkNamespace, operatorNamespace, resourceName, metaPluginsConfig, 0)
}

func createNetworkWithVlan(sriovDevice *sriovv1.InterfaceExt, sriovNetworkName, sriovNetworkNamespace, operatorNamespace, resourceName, metaPluginsConfig string, vlan int) {
	ipam := `{"type": "host-local","ranges": [[{"subnet": "1.1.1.0/24"}]],"dataDir": "/run/my-orchestrator/container-ipam-state"}`
	err := sriovnetwork.CreateSriovNetwork(testclient.SRIOVClient, sriovDevice, sriovNetworkName, sriovNetworkNamespace, operatorNamespace, resourceName, ipam, func(network *sriovv1.SriovNetwork) {
		if metaPluginsConfig != "" {
			network.Spec.MetaPluginsConfig = metaPluginsConfig
		}
		network.Spec.Vlan = vlan
	})
	Expect(err).ToNot(HaveOccurred())
	Eventually(func() error {
		netAttDef := &sriovk8sv1.NetworkAttachmentDefinition{}
		return testclient.SRIOVClient.Get(context.Background(), client.ObjectKey{Name: sriovNetworkName, Namespace: sriovNetworkNamespace}, netAttDef)
	}, time.Minute, 5*time.Second).ShouldNot(HaveOccurred())
}

// waitStable waits for the sriov setup to be stable after
// configuration modification.
func waitStable() {
	var snoTimeoutMultiplier time.Duration = 1
	isSNO, err := cluster.IsSingleNode()
	Expect(err).ToNot(HaveOccurred())
	if isSNO {
		snoTimeoutMultiplier = 2
	}
	// This used to be to check for sriov not to be stable first,
	// then stable. The issue is that if no configuration is applied, then
	// the status won't never go to not stable and the test will fail.
	// TODO: find a better way to handle this scenario
	time.Sleep(15 * time.Second)
	Eventually(func() bool {
		res, _ := sriovcluster.SriovStable("openshift-sriov-network-operator", testclient.SRIOVClient)
		// ignoring the error for the disconnected cluster scenario
		return res
	}, 20*time.Minute*snoTimeoutMultiplier, 1*time.Second).Should(BeTrue())

	Eventually(func() bool {
		isClusterReady, _ := sriovcluster.IsClusterStable(testclient.SRIOVClient)
		// ignoring the error for the disconnected cluster scenario
		return isClusterReady
	}, 20*time.Minute*snoTimeoutMultiplier, 1*time.Second).Should(BeTrue())
}

func getNodesWithPerformanceProfileAndSRIOV(sriovNodes []string) ([]string, error) {
	commonNodes := make([]string, 0)
	ppNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
	if err != nil {
		return nil, err
	}
	for _, ppNode := range ppNodes {
		for _, sriovNode := range sriovNodes {
			if ppNode.Name == sriovNode {
				commonNodes = append(commonNodes, ppNode.Name)
				break
			}
		}
	}
	return commonNodes, nil
}
