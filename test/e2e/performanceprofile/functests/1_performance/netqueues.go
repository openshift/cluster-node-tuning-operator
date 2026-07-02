package __performance

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

const tunedprofilesDirectory string = "/var/lib/ocp-tuned/profiles"

var _ = Describe("[ref_id: 40307][pao]Resizing Network Queues", Ordered, Label(string(label.Tier1)), func() {
	var workerRTNodes []corev1.Node
	var profile, initialProfile *performancev2.PerformanceProfile
	var tunedConfPath, performanceProfileName string
	var reservedCPUCount int
	var baselineMultiQueueNICs map[string]map[nodes.NodeInterface]int

	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		initialProfile = profile.DeepCopy()

		performanceProfileName = profile.Name

		reservedCPUs, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
		Expect(err).ToNot(HaveOccurred())
		reservedCPUCount = reservedCPUs.Size()

		tunedPaoProfile := fmt.Sprintf("openshift-node-performance-%s", performanceProfileName)
		//Verify the tuned profile is created on the worker-cnf nodes:
		// direct the error to /dev/null on purpose because tuneD always shows the following error:
		// "Cannot talk to TuneD daemon via DBus. Is TuneD daemon running?"
		// Which causes the test to fail, but it's a false-positive
		tunedCmd := []string{"/bin/sh", "-c", fmt.Sprintf("tuned-adm profile_info %s 2>/dev/null | grep ^openshift-", tunedPaoProfile)}
		for _, node := range workerRTNodes {
			tunedPod := nodes.TunedForNode(&node, RunningOnSingleNode)
			out, err := pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, tunedPod, tunedCmd)
			Expect(err).ToNot(HaveOccurred())
			profileNameFromTuned := testutils.ToString(out)
			Expect(profileNameFromTuned).To(Equal(tunedPaoProfile), "tuned profile created by PerformanceProfile %s does not exist under tuned", performanceProfileName)
		}

		tunedConfPath = filepath.Join(tunedprofilesDirectory, tunedPaoProfile, "tuned.conf")

		By("Discovering multi-queue capable NICs before any profile changes")
		baselineMultiQueueNICs = discoverMultiQueueNICs(context.TODO(), workerRTNodes)
		if len(baselineMultiQueueNICs) == 0 {
			Skip("No multi-queue capable NICs found on worker nodes")
		}

		By("Ensuring a baseline of UserLevelNetworking=true, no device filter")
		desiredNet := &performancev2.Net{UserLevelNetworking: ptr.To(true)}
		if !equality.Semantic.DeepEqual(profile.Spec.Net, desiredNet) {
			testlog.Infof("profile.Spec.Net differs from baseline, updating: current=%+v", profile.Spec.Net)
			profile.Spec.Net = desiredNet
			profiles.UpdateWithRetry(profile)
		}
	})

	AfterAll(func() {
		currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		if !equality.Semantic.DeepEqual(currentProfile.Spec, initialProfile.Spec) {
			By("Reverting to initial Profile")
			currentProfile.Spec = initialProfile.Spec
			profiles.UpdateWithRetry(currentProfile)
		}
	})

	Context("Updating performance profile for netqueues", func() {
		It("[test_id:40308][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Network device queues Should be set to the profile's reserved CPUs count", func() {
			By("To all non virtual network devices when no devices are specified under profile.Spec.Net.Devices")
			err := waitForNICsToMatchReservedCPU(context.TODO(), workerRTNodes, baselineMultiQueueNICs, reservedCPUCount)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})

		It("[test_id:40543] Add interfaceName and verify the interface netqueues are equal to reserved cpus count.", func() {
			nodeName, device := getRandomNodeDevice(baselineMultiQueueNICs)

			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable UserLevelNetworking and add Devices in Profile")
			profile.Spec.Net = &performancev2.Net{
				UserLevelNetworking: ptr.To(true),
				Devices: []performancev2.Device{
					{
						InterfaceName: &device.Name,
					},
				},
			}
			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			//Verify the tuned profile is created on the worker-cnf nodes:
			tunedCmd := []string{"bash", "-c",
				fmt.Sprintf("grep devices_udev_regex %s", tunedConfPath)}

			node, err := nodes.GetByName(nodeName)
			Expect(err).ToNot(HaveOccurred())
			tunedPod := nodes.TunedForNode(node, RunningOnSingleNode)

			Eventually(func() bool {
				out, err := pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, tunedPod, tunedCmd)
				if err != nil {
					return false
				}
				return strings.Contains(string(out), device.Name)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			err = waitForNICsToMatchReservedCPU(context.TODO(), workerRTNodes, baselineMultiQueueNICs, reservedCPUCount)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})

		It("[test_id:40545] Verify reserved cpus count is applied to specific supported networking devices using wildcard matches", func() {
			nodeName, device := getRandomNodeDevice(baselineMultiQueueNICs)
			devicePattern := device.Name[:len(device.Name)-1] + "*"

			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable UserLevelNetworking and add Devices in Profile")
			profile.Spec.Net = &performancev2.Net{
				UserLevelNetworking: ptr.To(true),
				Devices: []performancev2.Device{
					{
						InterfaceName: &devicePattern,
					},
				},
			}
			profiles.UpdateWithRetry(profile)

			//Verify the tuned profile is created on the worker-cnf nodes:
			tunedCmd := []string{"bash", "-c",
				fmt.Sprintf("grep devices_udev_regex %s", tunedConfPath)}

			node, err := nodes.GetByName(nodeName)
			Expect(err).ToNot(HaveOccurred())
			tunedPod := nodes.TunedForNode(node, RunningOnSingleNode)

			Eventually(func() bool {
				out, err := pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, tunedPod, tunedCmd)
				if err != nil {
					return false
				}
				return strings.Contains(string(out), device.Name)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			err = waitForNICsToMatchReservedCPU(context.TODO(), workerRTNodes, baselineMultiQueueNICs, reservedCPUCount)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})

		It("[test_id:72051] Verify reserved cpus count is applied to all but specific supported networking device using a negative match", func() {
			// Remove nodes with only one NIC as that cannot be used to check this behavior
			// this is done by removing the NIC entries to avoid deleting from the map while iterating
			nodesWithMultipleNICs := make(map[string]map[nodes.NodeInterface]int)
			for node, nics := range baselineMultiQueueNICs {
				if len(nics) >= 2 {
					nodesWithMultipleNICs[node] = nics
				}
			}
			if len(nodesWithMultipleNICs) == 0 {
				Skip("No nodes with multiple NICs available to test negative device match")
			}

			nodeName, device := getRandomNodeDevice(baselineMultiQueueNICs)
			devicePattern := "!" + device.Name

			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable UserLevelNetworking and add Devices in Profile")
			profile.Spec.Net = &performancev2.Net{
				UserLevelNetworking: ptr.To(true),
				Devices: []performancev2.Device{
					{
						InterfaceName: &devicePattern,
					},
				},
			}
			profiles.UpdateWithRetry(profile)

			//Verify the tuned profile is created on the worker-cnf nodes:
			tunedCmd := []string{"bash", "-c",
				fmt.Sprintf("grep devices_udev_regex %s", tunedConfPath)}

			node, err := nodes.GetByName(nodeName)
			Expect(err).ToNot(HaveOccurred())
			tunedPod := nodes.TunedForNode(node, RunningOnSingleNode)

			Eventually(func() bool {
				out, err := pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, tunedPod, tunedCmd)
				if err != nil {
					return false
				}
				return strings.Contains(string(out), device.Name)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			err = waitForNICsToMatchReservedCPU(context.TODO(), workerRTNodes, baselineMultiQueueNICs, reservedCPUCount)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})

		It("[test_id:40668] Verify reserved cpu count is added to networking devices matched with vendor and Device id", func() {
			nodeName, device := getRandomNodeDevice(baselineMultiQueueNICs)
			node, err := nodes.GetByName(nodeName)
			Expect(err).ToNot(HaveOccurred())

			vid := getVendorID(context.TODO(), *node, device.Name)
			did := getDeviceID(context.TODO(), *node, device.Name)

			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable UserLevelNetworking and add DeviceID, VendorID and Interface in Profile")
			profile.Spec.Net = &performancev2.Net{
				UserLevelNetworking: ptr.To(true),
				Devices: []performancev2.Device{
					{
						InterfaceName: &device.Name,
					},
					{
						VendorID: &vid,
						DeviceID: &did,
					},
				},
			}
			profiles.UpdateWithRetry(profile)

			//Verify the tuned profile is created on the worker-cnf nodes:
			tunedCmd := []string{"bash", "-c",
				fmt.Sprintf("grep devices_udev_regex %s", tunedConfPath)}

			node, err = nodes.GetByName(nodeName)
			Expect(err).ToNot(HaveOccurred())
			tunedPod := nodes.TunedForNode(node, RunningOnSingleNode)
			Eventually(func() bool {
				out, err := pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, tunedPod, tunedCmd)
				if err != nil {
					return false
				}
				return strings.Contains(string(out), device.Name)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			err = waitForNICsToMatchReservedCPU(context.TODO(), workerRTNodes, baselineMultiQueueNICs, reservedCPUCount)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})
	})
})

// waitForNICsToMatchReservedCPU polls the pre-discovered multi-queue NICs until all
// have their combined channel count equal to reservedCPUCount, indicating TuneD has
// applied the net queue configuration. Returns an error on timeout.
func waitForNICsToMatchReservedCPU(ctx context.Context, workerRTNodes []corev1.Node, baselineMultiQueueNICs map[string]map[nodes.NodeInterface]int, reservedCPUCount int) error {
	nodesByName := make(map[string]corev1.Node, len(workerRTNodes))
	for _, workerNode := range workerRTNodes {
		nodesByName[workerNode.Name] = workerNode
	}
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true,
		func(ctx context.Context) (bool, error) {
			for nodeName, nodeSupportedNics := range baselineMultiQueueNICs {
				node := nodesByName[nodeName]
				for supportedNic := range nodeSupportedNics {
					channels, err := getCombinedChannels(ctx, node, supportedNic)
					if err != nil || channels == 0 {
						testlog.Warningf("NIC %s on %s: unexpected error or unavailable: %v", supportedNic.Name, nodeName, err)
						return false, nil
					}
					if channels != reservedCPUCount {
						testlog.Infof("not all NICs ready - %s combined(%d) != reserved(%d), retrying (%s)", supportedNic.Name, channels, reservedCPUCount, nodeName)
						return false, nil
					}
				}
			}
			return true, nil
		})

	return err
}

// getCombinedChannels returns the current combined channel count for a NIC, or 0 if unsupported.
func getCombinedChannels(ctx context.Context, node corev1.Node, iface nodes.NodeInterface) (int, error) {
	if !iface.Physical {
		return 0, nil
	}
	cmdCombinedChannelsCurrent := []string{"bash", "-c",
		fmt.Sprintf("ethtool -l %s | sed -n '/Current hardware settings:/,/Combined:/{s/^Combined:\\s*//p}'", iface.Name)}
	out, err := nodes.ExecCommand(ctx, &node, cmdCombinedChannelsCurrent)
	if err != nil {
		testlog.Warningf("failed to get combined: exec error: %v", err)
		return 0, fmt.Errorf("ethtool exec failed: %w", err)
	}
	// sed extracts the Combined value: either "n/a" (unsupported) or a numeric string
	if strings.Contains(string(out), "n/a") {
		return 0, nil
	}
	combinedChannels, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		testlog.Warningf("failed to get combined: failed to parse combined channels: %v", err)
		return 0, fmt.Errorf("failed to parse combined channels: %w", err)
	}
	if combinedChannels <= 1 {
		testlog.Infof("combined not supported: (combined<=1)")
		return 0, nil
	}
	return combinedChannels, nil
}

// discoverMultiQueueNICs returns a snapshot of all NICs with combined channels > 1
// across the given nodes. Result is keyed by node name → NodeInterface → combined channel count.
// Returns an empty map if no qualifying NICs are found; errors are logged and the NIC is skipped.
func discoverMultiQueueNICs(ctx context.Context, workernodes []corev1.Node) map[string]map[nodes.NodeInterface]int {
	multiQueueNICs := make(map[string]map[nodes.NodeInterface]int)
	for _, node := range workernodes {
		interfaces, err := nodes.GetNodeInterfaces(ctx, node)
		if err != nil {
			testlog.Warningf("Failed to get interfaces on %s: %v", node.Name, err)
			continue
		}
		testlog.Infof("Discovering multi-queue NICs on %s", node.Name)
		nodeNICs := make(map[nodes.NodeInterface]int)
		for _, iface := range interfaces {
			channels, err := getCombinedChannels(ctx, node, iface)
			if err != nil {
				testlog.Warningf("%s: Couldn't get combined, skipping: %v", iface.Name, err)
				continue
			}
			if channels == 0 {
				continue
			}
			testlog.Infof("Discovered %s: multi-queue (combined=%d)", iface.Name, channels)
			nodeNICs[iface] = channels
		}
		if len(nodeNICs) > 0 {
			multiQueueNICs[node.Name] = nodeNICs
		}
	}
	return multiQueueNICs
}

func getVendorID(ctx context.Context, node corev1.Node, device string) string {
	cmd := []string{"bash", "-c",
		fmt.Sprintf("cat /sys/class/net/%s/device/vendor", device)}
	out, err := nodes.ExecCommand(ctx, &node, cmd)
	Expect(err).ToNot(HaveOccurred())
	stdout := testutils.ToString(out)
	return stdout
}

func getDeviceID(ctx context.Context, node corev1.Node, device string) string {
	cmd := []string{"bash", "-c",
		fmt.Sprintf("cat /sys/class/net/%s/device/device", device)}
	out, err := nodes.ExecCommand(ctx, &node, cmd)
	Expect(err).ToNot(HaveOccurred())
	stdout := testutils.ToString(out)
	return stdout
}

func getRandomNodeDevice(multiQueueNICs map[string]map[nodes.NodeInterface]int) (string, nodes.NodeInterface) {
	Expect(multiQueueNICs).ToNot(BeEmpty(), "getRandomNodeDevice: multiQueueNICs map is empty")
	for node, nics := range multiQueueNICs {
		for iface := range nics {
			return node, iface
		}
	}
	return "", nodes.NodeInterface{}
}
