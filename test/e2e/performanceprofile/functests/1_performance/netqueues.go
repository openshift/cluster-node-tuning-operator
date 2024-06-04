package __performance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

const tunedprofilesDirectory string = "/var/lib/ocp-tuned/profiles"

var _ = Describe("[ref_id: 40307][pao]Resizing Network Queues", Ordered, func() {
	var workerRTNodes []corev1.Node
	var profile, initialProfile *performancev2.PerformanceProfile
	var tunedConfPath, performanceProfileName string

	testutils.CustomBeforeAll(func() {
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

		tunedPaoProfile := fmt.Sprintf("openshift-node-performance-%s", performanceProfileName)
		//Verify the tuned profile is created on the worker-cnf nodes:
		tunedCmd := []string{"tuned-adm", "profile_info", tunedPaoProfile}
		for _, node := range workerRTNodes {
			tunedPod := nodes.TunedForNode(&node, RunningOnSingleNode)
			_, err := pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, tunedPod, tunedCmd)
			Expect(err).ToNot(HaveOccurred())
		}

		tunedConfPath = filepath.Join(tunedprofilesDirectory, tunedPaoProfile, "tuned.conf")
	})

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		if profile.Spec.Net == nil {
			By("Enable UserLevelNetworking in Profile")
			profile.Spec.Net = &performancev2.Net{
				UserLevelNetworking: pointer.Bool(true),
			}
			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)
		}
	})

	AfterEach(func() {
		By("Reverting the Profile")
		spec, err := json.Marshal(initialProfile.Spec)
		Expect(err).ToNot(HaveOccurred())
		Expect(testclient.Client.Patch(context.TODO(), profile,
			client.RawPatch(
				types.JSONPatchType,
				[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
			),
		)).ToNot(HaveOccurred())
	})

	Context("Updating performance profile for netqueues", func() {
		It("[test_id:40308][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Network device queues Should be set to the profile's reserved CPUs count", func() {
			nodesDevices := make(map[string]map[string]int)
			if profile.Spec.Net != nil {
				if profile.Spec.Net.UserLevelNetworking != nil && *profile.Spec.Net.UserLevelNetworking && len(profile.Spec.Net.Devices) == 0 {
					By("To all non virtual network devices when no devices are specified under profile.Spec.Net.Devices")
					err := checkDeviceSetWithReservedCPU(context.TODO(), workerRTNodes, nodesDevices, *profile)
					if err != nil {
						Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
					}
				}
			}
		})

		It("[test_id:40542] Verify the number of network queues of all supported network interfaces are equal to reserved cpus count", func() {
			nodesDevices := make(map[string]map[string]int)
			err := checkDeviceSetWithReservedCPU(context.TODO(), workerRTNodes, nodesDevices, *profile)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})

		It("[test_id:40543] Add interfaceName and verify the interface netqueues are equal to reserved cpus count.", func() {
			nodesDevices := make(map[string]map[string]int)
			deviceSupport, err := checkDeviceSupport(context.TODO(), workerRTNodes, nodesDevices)
			Expect(err).ToNot(HaveOccurred())
			if !deviceSupport {
				Skip("Skipping Test: There are no supported Network Devices")
			}
			nodeName, device := getRandomNodeDevice(nodesDevices)
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			if profile.Spec.Net.UserLevelNetworking != nil && *profile.Spec.Net.UserLevelNetworking && len(profile.Spec.Net.Devices) == 0 {
				By("Enable UserLevelNetworking and add Devices in Profile")
				profile.Spec.Net = &performancev2.Net{
					UserLevelNetworking: pointer.Bool(true),
					Devices: []performancev2.Device{
						{
							InterfaceName: &device,
						},
					},
				}
				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)
			}
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
				return strings.ContainsAny(string(out), device)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			nodesDevices = make(map[string]map[string]int)
			err = checkDeviceSetWithReservedCPU(context.TODO(), workerRTNodes, nodesDevices, *profile)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})

		It("[test_id:40545] Verify reserved cpus count is applied to specific supported networking devices using wildcard matches", func() {
			nodesDevices := make(map[string]map[string]int)
			var device, devicePattern string
			deviceSupport, err := checkDeviceSupport(context.TODO(), workerRTNodes, nodesDevices)
			Expect(err).ToNot(HaveOccurred())
			if !deviceSupport {
				Skip("Skipping Test: There are no supported Network Devices")
			}
			nodeName, device := getRandomNodeDevice(nodesDevices)
			devicePattern = device[:len(device)-1] + "*"
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			if profile.Spec.Net.UserLevelNetworking != nil && *profile.Spec.Net.UserLevelNetworking && len(profile.Spec.Net.Devices) == 0 {
				By("Enable UserLevelNetworking and add Devices in Profile")
				profile.Spec.Net = &performancev2.Net{
					UserLevelNetworking: pointer.Bool(true),
					Devices: []performancev2.Device{
						{
							InterfaceName: &devicePattern,
						},
					},
				}
				profiles.UpdateWithRetry(profile)
			}
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
				return strings.ContainsAny(string(out), device)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			nodesDevices = make(map[string]map[string]int)
			err = checkDeviceSetWithReservedCPU(context.TODO(), workerRTNodes, nodesDevices, *profile)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})

		It("[test_id:72051] Verify reserved cpus count is applied to all but specific supported networking device using a negative match", func() {
			nodesDevices := make(map[string]map[string]int)
			var device, devicePattern string
			deviceSupport, err := checkDeviceSupport(context.TODO(), workerRTNodes, nodesDevices)
			Expect(err).ToNot(HaveOccurred())
			if !deviceSupport {
				Skip("Skipping Test: There are no supported Network Devices")
			}

			// Remove nodes with only one NIC as that cannot be used to check this behavior
			// this is done by removing the NIC entries to avoid deleting from the map while iterating
			for node, nics := range nodesDevices {
				if len(nics) < 2 {
					nodesDevices[node] = nil
				}
			}

			nodeName, device := getRandomNodeDevice(nodesDevices)
			devicePattern = "!" + device
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			if profile.Spec.Net.UserLevelNetworking != nil && *profile.Spec.Net.UserLevelNetworking && len(profile.Spec.Net.Devices) == 0 {
				By("Enable UserLevelNetworking and add Devices in Profile")
				profile.Spec.Net = &performancev2.Net{
					UserLevelNetworking: pointer.Bool(true),
					Devices: []performancev2.Device{
						{
							InterfaceName: &devicePattern,
						},
					},
				}
				profiles.UpdateWithRetry(profile)
			}
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
				return strings.ContainsAny(string(out), device)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			nodesDevices = make(map[string]map[string]int)
			err = checkDeviceSetWithReservedCPU(context.TODO(), workerRTNodes, nodesDevices, *profile)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}

			// After at least one NIC was configured, make sure that the selected NIC was NOT it

			Expect(nodesDevices).To(HaveKey(node.Name))
			Expect(nodesDevices[node.Name]).To(HaveKey(device))
			Expect(nodesDevices[node.Name][device]).ToNot(Equal(getReservedCPUSize(profile.Spec.CPU)))
		})

		It("[test_id:40668] Verify reserved cpu count is added to networking devices matched with vendor and Device id", func() {
			nodesDevices := make(map[string]map[string]int)
			deviceSupport, err := checkDeviceSupport(context.TODO(), workerRTNodes, nodesDevices)
			Expect(err).ToNot(HaveOccurred())
			if !deviceSupport {
				Skip("Skipping Test: There are no supported Network Devices")
			}
			nodeName, device := getRandomNodeDevice(nodesDevices)
			node, err := nodes.GetByName(nodeName)
			Expect(err).ToNot(HaveOccurred())
			vid := getVendorID(context.TODO(), *node, device)
			did := getDeviceID(context.TODO(), *node, device)
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			if profile.Spec.Net.UserLevelNetworking != nil && *profile.Spec.Net.UserLevelNetworking && len(profile.Spec.Net.Devices) == 0 {
				By("Enable UserLevelNetworking and add DeviceID, VendorID and Interface in Profile")
				profile.Spec.Net = &performancev2.Net{
					UserLevelNetworking: pointer.Bool(true),
					Devices: []performancev2.Device{
						{
							InterfaceName: &device,
						},
						{
							VendorID: &vid,
							DeviceID: &did,
						},
					},
				}
				profiles.UpdateWithRetry(profile)
			}
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
				return strings.ContainsAny(string(out), device)
			}, cluster.ComputeTestTimeout(2*time.Minute, RunningOnSingleNode), 5*time.Second).Should(BeTrue(), "could not get a tuned profile set with devices_udev_regex")

			nodesDevices = make(map[string]map[string]int)
			err = checkDeviceSetWithReservedCPU(context.TODO(), workerRTNodes, nodesDevices, *profile)
			if err != nil {
				Skip("Skipping Test: Unable to set Network queue size to reserved cpu count")
			}
		})
	})
})

// Check a device that supports multiple queues and set with with reserved CPU size exists
func checkDeviceSetWithReservedCPU(ctx context.Context, workerRTNodes []corev1.Node, nodesDevices map[string]map[string]int, profile performancev2.PerformanceProfile) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 90*time.Second, true, func(ctx context.Context) (bool, error) {
		deviceSupport, err := checkDeviceSupport(ctx, workerRTNodes, nodesDevices)
		Expect(err).ToNot(HaveOccurred())
		if !deviceSupport {
			return false, nil
		}
		for _, devices := range nodesDevices {
			for _, size := range devices {
				if size == getReservedCPUSize(profile.Spec.CPU) {
					return true, nil
				}
			}
		}
		return false, nil
	})
}

// Check if the device support multiple queues
func checkDeviceSupport(ctx context.Context, workernodes []corev1.Node, nodesDevices map[string]map[string]int) (bool, error) {
	cmdGetPhysicalDevices := []string{"find", "/sys/class/net", "-type", "l", "-not", "-lname", "*virtual*", "-printf", "%f "}
	var channelCurrentCombined int
	var noSupportedDevices = true
	var err error
	for _, node := range workernodes {
		if nodesDevices[node.Name] == nil {
			nodesDevices[node.Name] = make(map[string]int)
		}
		tunedPod := nodes.TunedForNode(&node, RunningOnSingleNode)
		phyDevs, err := pods.WaitForPodOutput(ctx, testclient.K8sClient, tunedPod, cmdGetPhysicalDevices)
		Expect(err).ToNot(HaveOccurred())
		for _, d := range strings.Split(string(phyDevs), " ") {
			if d == "" {
				continue
			}
			_, err := pods.WaitForPodOutput(ctx, testclient.K8sClient, tunedPod, []string{"ethtool", "-l", d})
			if err == nil {
				cmdCombinedChannelsCurrent := []string{"bash", "-c",
					fmt.Sprintf("ethtool -l %s | sed -n '/Current hardware settings:/,/Combined:/{s/^Combined:\\s*//p}'", d)}
				out, err := pods.WaitForPodOutput(ctx, testclient.K8sClient, tunedPod, cmdCombinedChannelsCurrent)
				if strings.Contains(string(out), "n/a") {
					fmt.Printf("Device %s doesn't support multiple queues\n", d)
				} else {
					channelCurrentCombined, err = strconv.Atoi(strings.TrimSpace(string(out)))
					if err != nil {
						testlog.Warningf(fmt.Sprintf("unable to retrieve current multi-purpose channels hardware settings for device %s on %s",
							d, node.Name))
					}
					if channelCurrentCombined == 1 {
						fmt.Printf("Device %s doesn't support multiple queues\n", d)
					} else {
						fmt.Printf("Device %s supports multiple queues\n", d)
						nodesDevices[node.Name][d] = channelCurrentCombined
						noSupportedDevices = false
					}
				}
			}
		}
	}
	if noSupportedDevices {
		return false, err
	}
	return true, err
}

func getReservedCPUSize(CPU *performancev2.CPU) int {
	reservedCPUs, err := cpuset.Parse(string(*CPU.Reserved))
	Expect(err).ToNot(HaveOccurred())
	return reservedCPUs.Size()
}

func getVendorID(ctx context.Context, node corev1.Node, device string) string {
	cmd := []string{"bash", "-c",
		fmt.Sprintf("cat /sys/class/net/%s/device/vendor", device)}
	stdout, err := nodes.ExecCommandToString(ctx, cmd, &node)
	Expect(err).ToNot(HaveOccurred())
	return stdout
}

func getDeviceID(ctx context.Context, node corev1.Node, device string) string {
	cmd := []string{"bash", "-c",
		fmt.Sprintf("cat /sys/class/net/%s/device/device", device)}
	stdout, err := nodes.ExecCommandToString(ctx, cmd, &node)
	Expect(err).ToNot(HaveOccurred())
	return stdout
}

func getRandomNodeDevice(nodesDevices map[string]map[string]int) (string, string) {
	node := ""
	device := ""
	for n := range nodesDevices {
		node = n
		for d := range nodesDevices[node] {
			if d != "" {
				device = d
				return node, device
			}
		}
	}
	return node, device
}
