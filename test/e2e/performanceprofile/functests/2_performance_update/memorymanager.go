package __performance_update

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/events"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	cgroupRoot string = "/rootfs/sys/fs/cgroup"
)

// MMPod Memory Manager Pod Definition
type MMPod struct {
	podV1Struct                   *corev1.Pod
	namespace                     string
	cpu, memory, noOfhpgs, medium string
	hpgSize                       performancev2.HugePageSize
}

var _ = Describe("[rfe_id: 43186][memorymanager] Memorymanager feature", Label(string(label.MemoryManager)), func() {
	var (
		workerRTNodes           []corev1.Node
		profile, initialProfile *performancev2.PerformanceProfile
		poolName                string
		err                     error
		ctx                     context.Context = context.Background()
	)

	Context("Group Both Numa Nodes with restricted topology", Ordered, Label(string(label.Tier2)), func() {
		var numaCoreSiblings map[int]map[int][]int
		var reserved, isolated cpuset.CPUSet
		// Number of hugepages of size 2M created on both numa nodes
		const hpCount = 20
		testutils.CustomBeforeAll(func() {
			var policy = "restricted"
			workerRTNodes = getUpdatedNodes()
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			// Save the original performance profile
			initialProfile = profile.DeepCopy()

			poolName = poolname.GetByProfile(ctx, profile)

			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", node.Name))
				}
			}

			By("Modifying Profile")
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(context.TODO(), &node)
			}
			// Get cpu siblings from core 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, reservedCores)
				reserved = reserved.Union(cpusiblings)
			}
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, k)
					isolated = isolated.Union(cpusiblings)
				}
			}
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())

			hpSize1G := performancev2.HugePageSize("1G")
			hpSize2M := performancev2.HugePageSize("2M")

			requiredHugepages := &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize1G,
				Pages: []performancev2.HugePage{
					{
						Count: int32(hpCount),
						Size:  hpSize2M,
					},
				},
			}
			profile.Spec.HugePages = requiredHugepages
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			By("Updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)

		})

		// Automates OCPBUGS-75
		It("[test_id:60545] Reject guaranteed pod requesting resources that cannot be satisfied by 2 numa nodes together", func() {
			var mm1 MMPod
			mm1.memory = "200Mi"
			mm1.cpu = "2"
			mm1.noOfhpgs = "24Mi"
			mm1.hpgSize = profile.Spec.HugePages.Pages[0].Size
			guPod := true
			testPod := mm1.createPodTemplate(profile, guPod, &workerRTNodes[0])
			By("creating test pod")
			err = testclient.DataPlaneClient.Create(context.TODO(), testPod)
			Expect(err).ToNot(HaveOccurred(), "Failed to create test pod")
			testPod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testPod), corev1.PodConditionType(corev1.PodFailed), corev1.ConditionFalse, 2*time.Minute)
			// Even though number of hugepage requests can be satisfied by 2 numa nodes together
			// Number of cpus are only 2 which only requires 1 numa node , So minimum number of numa nodes needed to satisfy is only 1.
			// According to Restricted TM policy: only allow allocations from the minimum number of NUMA nodes.
			// Look at each resource request, see what the minimum number of NUMA nodes are required to
			// satisfy that resource request. Allow alignment to that number of NUMA nodes for all resources.
			// Hence the pod should fail with TopologyAffinityError
			err := checkPodEvent(testPod, "TopologyAffinityError")
			Expect(err).ToNot(HaveOccurred())
			Expect(testPod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")
			Expect(mm1.removePod(context.TODO(), testPod)).ToNot(HaveOccurred(), "Failed to remove test pod")
		})

		It("[test_id:60694] Accept guaranteed pod requesting resources that can be satisfied by 2 numa nodes together", func() {
			var mm2 MMPod
			mm2.hpgSize = profile.Spec.HugePages.Pages[0].Size
			targetNode := &workerRTNodes[0]
			mm2.memory = "200Mi"
			mm2.cpu = fmt.Sprintf("%d", isolated.Size()-2)
			// no. of hugepages is 20 * 2 (numazones). 40Mi
			// we are asking for 30Mi, so it needs 2 numazones combined to
			// satisfy the requirement
			mm2.noOfhpgs = "30Mi"
			testPod := mm2.createPodTemplate(profile, true, targetNode)
			// Initialize test pod, check if the pod uses both numa node  0 and 1
			err := initializePod(context.TODO(), testPod)
			Expect(err).ToNot(HaveOccurred(), "unable to initialize Pod")
			numaZone, err := GetMemoryNodes(context.TODO(), testPod, targetNode)
			// Expect both numa nodes to be used by pod
			Expect(numaZone).To(Equal("0-1"))
			Expect(err).ToNot(HaveOccurred(), "Pod's numa affinity is %s instead of %s", numaZone, "0-1")
			Expect(mm2.removePod(context.TODO(), testPod)).ToNot(HaveOccurred(), "Failed to remove test pod")
		})

		It("[test_id:60695] Allow burstable pod with hugepages", func() {
			var mm2 MMPod
			mm2.hpgSize = profile.Spec.HugePages.Pages[0].Size
			targetNode := &workerRTNodes[0]
			mm2.memory = "200Mi"
			mm2.cpu = fmt.Sprintf("%d", isolated.Size()-2)
			mm2.noOfhpgs = "8Mi"
			testPod := mm2.createPodTemplate(profile, false, targetNode)
			// Initialize test pod, check if the pod uses both numa node  0 and 1
			err := initializePod(context.TODO(), testPod) // "0-1", targetNode)
			Expect(err).ToNot(HaveOccurred(), "Unable to initialize pod")
			numaZone, err := GetMemoryNodes(context.TODO(), testPod, targetNode)
			Expect(err).ToNot(HaveOccurred(), "Unable to fetch numa zone")
			// Expect both numa nodes to be used by pod
			Expect(numaZone).To(Equal("0-1"), "Pod's numa affinity is %s instead of %s", numaZone, "0-1")
			Expect(mm2.removePod(context.TODO(), testPod)).ToNot(HaveOccurred(), "Failed to remove test pod")
		})

		AfterAll(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if !equality.Semantic.DeepEqual(currentSpec, spec) {
				profiles.UpdateWithRetry(initialProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, initialProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, initialProfile)
			}
		})
	})

	Context("Numa Nodes of same Hugepage size with different hugepages count and restricted policy", Ordered, Label(string(label.Tier2)), func() {
		var numaCoreSiblings map[int]map[int][]int
		var reserved, isolated, available_node0_cpus, available_node1_cpus cpuset.CPUSet
		var numaZone0HugepagesCount int = 10
		var numaZone1HugepagesCount int = 20
		numaZone := make(map[int]map[int]cpuset.CPUSet)
		testutils.CustomBeforeAll(func() {
			var policy = "restricted"
			workerRTNodes = getUpdatedNodes()
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred(), "unable to get performance profile")
			poolName = poolname.GetByProfile(ctx, profile)

			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", node.Name))
				}
			}
			// Save the original performance profile
			initialProfile = profile.DeepCopy()
			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred(), "Unable to get numa information from the node")
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", node.Name))
				}
			}

			By("Modifying Profile")
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(context.TODO(), &node)
			}

			// Get cpu siblings from Numa Node 0
			count := 0
			for reservedCores := range numaCoreSiblings[0] {
				if count > 1 {
					break
				}
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, reservedCores)
				reserved = reserved.Union(cpusiblings)
				count++
			}

			for key := range numaCoreSiblings {
				if numaZone[key] == nil {
					numaZone[key] = make(map[int]cpuset.CPUSet)
				}
				for k := range numaCoreSiblings[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, k)
					isolated = isolated.Union(cpusiblings)
					numaZone[key][k] = numaZone[key][k].Union(cpusiblings)
				}
			}

			// save the assigned cpus to isolated in a map based on zone and core
			// we need to know which and how many cpus are available after cpus
			// are assigned to reserved.
			// Get available cpus in numa node 0 and numa node 1
			for core := range numaZone[0] {
				available_node0_cpus = available_node0_cpus.Union(numaZone[0][core])
			}

			for core := range numaZone[1] {
				available_node1_cpus = available_node1_cpus.Union(numaZone[1][core])
			}

			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())

			// Enable Hugepages
			hpSize2M := performancev2.HugePageSize("2M")
			hpSize1G := performancev2.HugePageSize("1G")
			profile.Spec.HugePages = &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize1G,
				Pages: []performancev2.HugePage{
					{
						Count: int32(numaZone0HugepagesCount),
						Size:  hpSize2M,
						Node:  ptr.To(int32(0)),
					},
					{
						Count: int32(numaZone1HugepagesCount),
						Size:  hpSize2M,
						Node:  ptr.To(int32(1)),
					},
				},
			}
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			By("Updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)
		})

		It("[test_id:60696] Verify Guaranteed Pod has right numa affinity", func() {
			var mm1 MMPod
			mm1.memory = "200Mi"
			targetNode := &workerRTNodes[0]
			mm1.hpgSize = profile.Spec.HugePages.Pages[1].Size
			// cpus of numa zone1 will be greater than numa zone0 because we used 4 cpus from numa zone0 for reserved.
			// so number of cpus will be total number of available cpus on numazone0 + 2
			// which can be satisfied by cpus of numa zone 1 only.
			mm1.cpu = fmt.Sprintf("%d", available_node0_cpus.Size()+2)
			// we are requesting 14Mi hugepages which again can be satisfied by numa zone 1
			// since numa zone 0 has only 10Mi hugepages
			mm1.noOfhpgs = "14Mi"
			testPod := mm1.createPodTemplate(profile, true, targetNode)
			// Initialize test pod, check if the pod uses only numa node 1
			err := initializePod(context.TODO(), testPod)
			Expect(err).ToNot(HaveOccurred(), "Unable to initialize pod")
			numaZone, err := GetMemoryNodes(context.TODO(), testPod, targetNode)
			// Expect numa node 1 to be used by pod
			Expect(numaZone).To(Equal("1"))
			Expect(err).ToNot(HaveOccurred(), "Pod's numa affinity is %s instead of %s", numaZone, "1")
			// Delete pod
			Expect(mm1.removePod(context.TODO(), testPod)).ToNot(HaveOccurred(), "Failed to remove test pod")
		})

		It("[test_id:60697] Verify Pod is rejected when the numa zone doesn't have enough resources", func() {
			// We first create a pod thats assigned to numa zone 1
			// and requesting most of the hugepages resources from numa zone 1
			var mm1, mm2 MMPod
			targetNode := &workerRTNodes[0]
			mm1.memory = "200Mi"
			mm1.hpgSize = profile.Spec.HugePages.Pages[1].Size
			var availableCpusOnZone1 = available_node1_cpus.Size()
			// cpus of numa zone1 will be greater than numa zone0 because  we used 4 cpus from numa zone0 for reserved.
			// so number of cpus will be total number of available cpus on numazone 0 + 2
			// which can be satisfied by cpus of numa zone 1 only.
			mm1.cpu = fmt.Sprintf("%d", available_node0_cpus.Size()+2)
			// Reduce the cpus taken for testPod1 from availablecpus on numa zone1
			availableCpusOnZone1 = availableCpusOnZone1 - 2
			// we are requesting 14Mi hugepages which again can be satisfed by numa zone 1
			// since numa zone 0 has only 10Mi hugepages
			mm1.noOfhpgs = "14Mi"
			testPod1 := mm1.createPodTemplate(profile, true, targetNode)
			// Initialize test pod, check if the pod numa affinity is 1
			err := initializePod(context.TODO(), testPod1)
			Expect(err).ToNot(HaveOccurred(), "Unable to initialize pod")
			numaZone, err := GetMemoryNodes(context.TODO(), testPod1, targetNode)
			// Expect numa node 1 to be used by pod
			Expect(numaZone).To(Equal("1"))
			Expect(err).ToNot(HaveOccurred(), "Pod's numa affinity is %s instead of %s", numaZone, "1")
			// Create another pod asking for resources from numaZone2
			mm2.memory = "200Mi"
			mm2.hpgSize = profile.Spec.HugePages.Pages[1].Size
			mm2.cpu = fmt.Sprintf("%d", availableCpusOnZone1)
			mm2.noOfhpgs = "10Mi"
			testPod2 := mm2.createPodTemplate(profile, true, targetNode)
			By("creating test pod")
			err = testclient.DataPlaneClient.Create(context.TODO(), testPod2)
			Expect(err).ToNot(HaveOccurred(), "failed to create testpod2")
			testPod2, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testPod2), corev1.PodConditionType(corev1.PodFailed), corev1.ConditionTrue, 2*time.Minute)
			Expect(err).To(HaveOccurred(), "testpod2 did not go in to failed condition")
			err = checkPodEvent(testPod2, "FailedScheduling")
			Expect(err).ToNot(HaveOccurred(), "failed to find expected event: failedScheduling")
			// Delete pods
			Expect(mm1.removePod(context.TODO(), testPod1)).ToNot(HaveOccurred(), "Failed to remove testpod1")
			Expect(mm2.removePod(context.TODO(), testPod2)).ToNot(HaveOccurred(), "Failed to remove testpod2")
		})

		AfterAll(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if !equality.Semantic.DeepEqual(currentSpec, spec) {
				By("updating Performance profile")
				profiles.UpdateWithRetry(initialProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, initialProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, initialProfile)
			}
		})
	})

	Context("Group Both Numa Nodes with single-numa-node topology", Ordered, Label(string(label.Tier2)), func() {
		var numaCoreSiblings map[int]map[int][]int
		var reserved, isolated cpuset.CPUSet
		var err error
		// Number of hugepages of size 2M
		const hpCount = 20
		testutils.CustomBeforeAll(func() {
			var policy = "single-numa-node"
			workerRTNodes = getUpdatedNodes()
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			poolName = poolname.GetByProfile(ctx, profile)
			// Save the original performance profile
			initialProfile = profile.DeepCopy()

			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", node.Name))
				}
			}

			By("Modifying Profile")
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(context.TODO(), &node)
			}
			// Get cpu siblings from core 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, reservedCores)
				reserved = reserved.Union(cpusiblings)
			}
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, k)
					isolated = isolated.Union(cpusiblings)
				}
			}
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())

			// Enable Hugepages
			hpSize2M := performancev2.HugePageSize("2M")
			hpSize1G := performancev2.HugePageSize("1G")
			profile.Spec.HugePages = &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize1G,
				Pages: []performancev2.HugePage{
					{
						Count: int32(hpCount),
						Size:  hpSize2M,
					},
				},
			}
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			By("Updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)
		})

		It("[test_id:60698] Reject Guaranteed pod requesting resources from 2 numa nodes together", func() {
			var mm1 MMPod
			mm1.memory = "200Mi"
			mm1.cpu = "2"
			mm1.noOfhpgs = "24Mi"
			mm1.hpgSize = profile.Spec.HugePages.Pages[0].Size
			guPod := true
			testPod := mm1.createPodTemplate(profile, guPod, &workerRTNodes[0])
			By("creating test pod")
			err = testclient.DataPlaneClient.Create(context.TODO(), testPod)
			Expect(err).ToNot(HaveOccurred(), "failed to create testpod")
			testPod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testPod), corev1.PodConditionType(corev1.PodFailed), corev1.ConditionFalse, 2*time.Minute)
			err := checkPodEvent(testPod, "TopologyAffinityError")
			Expect(err).ToNot(HaveOccurred(), "pod did not fail with TopologyAffinityError")
			Expect(testPod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")
			Expect(mm1.removePod(context.TODO(), testPod)).ToNot(HaveOccurred(), "Failed to remove test pod")
		})
		AfterAll(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if !equality.Semantic.DeepEqual(currentSpec, spec) {
				By("updating Performance profile")
				profiles.UpdateWithRetry(initialProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, initialProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, initialProfile)
			}
		})
	})

	Context("Numa Nodes with different hugepage size and single-numa-node policy", Ordered, Label(string(label.Tier2)), func() {
		var numaCoreSiblings map[int]map[int][]int
		var reserved, isolated, available_node0_cpus, available_node1_cpus cpuset.CPUSet
		var numaZone0HugepagesCount int = 10
		var numaZone1HugepagesCount int = 10
		var err error
		numaZone := make(map[int]map[int]cpuset.CPUSet)
		testutils.CustomBeforeAll(func() {
			var policy = "single-numa-node"
			workerRTNodes = getUpdatedNodes()
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred(), "failed to fetch performance profile")
			poolName = poolname.GetByProfile(ctx, profile)
			// Save the original performance profile
			initialProfile = profile.DeepCopy()

			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", node.Name))
				}
			}

			By("Modifying Profile")
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(context.TODO(), &node)
			}

			// Get cpu siblings from core 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, reservedCores)
				reserved = reserved.Union(cpusiblings)
			}

			for key := range numaCoreSiblings {
				if numaZone[key] == nil {
					numaZone[key] = make(map[int]cpuset.CPUSet)
				}
				for k := range numaCoreSiblings[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, k)
					isolated = isolated.Union(cpusiblings)
					numaZone[key][k] = numaZone[key][k].Union(cpusiblings)
				}
			}

			// save the assigned cpus to isolated in a map based on zone and core
			// we need to know which and how many cpus are available after cpus
			// are assigned to reserved.
			// Get available cpus in numa node 0 and numa node 1
			for core := range numaZone[0] {
				available_node0_cpus = available_node0_cpus.Union(numaZone[0][core])
			}

			for core := range numaZone[1] {
				available_node1_cpus = available_node0_cpus.Union(numaZone[1][core])
			}

			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())

			// Enable Hugepages
			hpSize2M := performancev2.HugePageSize("2M")
			hpSize1G := performancev2.HugePageSize("1G")
			profile.Spec.HugePages = &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize1G,
				Pages: []performancev2.HugePage{
					{
						Count: int32(numaZone0HugepagesCount),
						Size:  hpSize2M,
						Node:  ptr.To(int32(0)),
					},
					{
						Count: int32(numaZone1HugepagesCount),
						Size:  hpSize1G,
						Node:  ptr.To(int32(1)),
					},
				},
			}
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			By("Updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)
		})

		It("[test_id:37150] Verify Guaranteed Pod has right numa affinity", func() {
			var mm1, mm2 MMPod
			mm1.memory = "200Mi"
			mm1.hpgSize = profile.Spec.HugePages.Pages[0].Size
			targetNode := &workerRTNodes[0]
			mm1.cpu = fmt.Sprintf("%d", available_node0_cpus.Size()-2)
			mm1.hpgSize = "2M"
			// we are requesting 8Mi hugepages which again can be satisfied by numa zone 0
			mm1.noOfhpgs = "8Mi"
			testPod1 := mm1.createPodTemplate(profile, true, targetNode)
			// Initialize test pod, check if the pod uses Numa node 0
			err := initializePod(context.TODO(), testPod1)
			Expect(err).ToNot(HaveOccurred(), "Unable to initialize pod")
			numaZone, err := GetMemoryNodes(context.TODO(), testPod1, targetNode)
			// Expect numa node 0 to be used by pod
			Expect(numaZone).To(Equal("0"))
			Expect(err).ToNot(HaveOccurred(), "Pod's numa affinity is %s instead of %s", numaZone, "0")
			// Delete pod
			Expect(mm1.removePod(context.TODO(), testPod1)).ToNot(HaveOccurred(), "Failed to remove testpod1")
			// Schedule pod on numa zone 1
			mm2.noOfhpgs = "4Gi"
			mm2.memory = "200Mi"
			mm2.hpgSize = profile.Spec.HugePages.Pages[1].Size
			mm2.cpu = fmt.Sprintf("%d", available_node1_cpus.Size()-2)
			testPod2 := mm2.createPodTemplate(profile, true, targetNode)
			// Initialize test pod, check if the pod uses Numa node 1
			err = initializePod(context.TODO(), testPod2)
			Expect(err).ToNot(HaveOccurred(), "Unable to initialize pod")
			numaZone, err = GetMemoryNodes(context.TODO(), testPod2, targetNode)
			// Expect numa node 1 to be used by pod
			Expect(numaZone).To(Equal("1"))
			Expect(err).ToNot(HaveOccurred(), "Pod's numa affinity is %s instead of %s", numaZone, "1")
			// Delete pod
			Expect(mm2.removePod(context.TODO(), testPod2)).ToNot(HaveOccurred(), "Failed to remove test pod")
		})

		AfterAll(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if !equality.Semantic.DeepEqual(currentSpec, spec) {
				By("updating Performance profile")
				profiles.UpdateWithRetry(initialProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, initialProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, initialProfile)
			}
		})
	})
})

func checkPodEvent(testpod *corev1.Pod, podEventReason string) error {
	podEvents, err := events.GetEventsForObject(testclient.DataPlaneClient, testpod.Namespace, testpod.Name, string(testpod.UID))
	if err != nil {
		testlog.Error(err)
		return err
	}
	testlog.Infof("log pod %s/%s events to verify Event: %s", testpod.Namespace, testpod.Name, podEventReason)
	reasons := []string{}
	for _, event := range podEvents.Items {
		testlog.Warningf("-> %s %s %s", event.Action, event.Reason, event.Message)
		reasons = append(reasons, event.Reason)
	}
	truepodAffinity := false
	for _, v := range reasons {
		if v == podEventReason {
			truepodAffinity = true
		}
	}
	Expect(truepodAffinity).To(BeTrue())
	return nil
}

func (mm MMPod) createPodTemplate(profile *performancev2.PerformanceProfile, gu bool, targetNode *corev1.Node) *corev1.Pod {
	testNode := make(map[string]string)
	testNode["kubernetes.io/hostname"] = targetNode.Name
	mm.podV1Struct = pods.GetTestPod()
	mm.podV1Struct.Namespace = testutils.NamespaceTesting
	mm.namespace = testutils.NamespaceTesting
	volumeName := fmt.Sprintf("hugepage-%si", mm.hpgSize)
	mm.medium = fmt.Sprintf("HugePages-%si", mm.hpgSize)
	mm.podV1Struct.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(mm.memory),
			corev1.ResourceName(fmt.Sprintf("hugepages-%si", mm.hpgSize)): resource.MustParse(mm.noOfhpgs),
		},
	}
	if gu {
		mm.podV1Struct.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = resource.MustParse(mm.cpu)
	}

	// add hugepage volume mount to pod spec
	mm.podV1Struct.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
		*mm.CreateHugePagesVolumeMounts(),
	}

	mm.podV1Struct.Spec.Volumes = []corev1.Volume{
		{
			Name: strings.ToLower(volumeName),
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMedium(mm.medium),
				},
			},
		},
	}
	// we set the node selector to worker-cnf node
	mm.podV1Struct.Spec.NodeSelector = testNode
	// Set runtimeclass
	runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	mm.podV1Struct.Spec.RuntimeClassName = &runtimeClass
	return mm.podV1Struct
}

// removePod Delete test pod
func (mm MMPod) removePod(ctx context.Context, testPod *corev1.Pod) error {
	err := testclient.DataPlaneClient.Get(ctx, client.ObjectKeyFromObject(testPod), testPod)
	if errors.IsNotFound(err) {
		return err
	}
	err = testclient.DataPlaneClient.Delete(ctx, testPod)
	if err != nil {
		return err
	}

	return pods.WaitForDeletion(ctx, testPod, pods.DefaultDeletionTimeout*time.Second)
}

// InitializePod initialize pods which we want to be in running state
func initializePod(ctx context.Context, testPod *corev1.Pod) error {
	err := testclient.DataPlaneClient.Create(context.TODO(), testPod)
	if err != nil {
		testlog.Errorf("Failed to create test pod %v", testPod)
	}
	testPod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testPod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
	if err != nil {
		testlog.Errorf("%v failed to start", testPod)
	}
	err = checkPodEvent(testPod, "Scheduled")
	if err != nil {
		testlog.Errorf("%v did not schedule", testPod)
	}
	return err
}

// GetMemoryNodes Returns memory nodes used by the pods' container
func GetMemoryNodes(ctx context.Context, testPod *corev1.Pod, targetNode *corev1.Node) (string, error) {
	var containerCgroup, memoryNodes, fullPath, cpusetMemsPath string
	containerID, err := pods.GetContainerIDByName(testPod, "test")
	if err != nil {
		return "", fmt.Errorf("Failed to fetch containerId for %v", testPod)
	}
	pid, err := nodes.ContainerPid(context.TODO(), targetNode, containerID)
	if err != nil {
		return "", fmt.Errorf("Unable to get container PID: %v", err)
	}
	cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pid)}
	out, err := nodes.ExecCommand(context.TODO(), targetNode, cmd)
	if err != nil {
		return "", err
	}
	containerCgroup, err = cgroup.PidParser(out)
	Expect(err).ToNot(HaveOccurred())
	testlog.Infof("cgroup path = %s", containerCgroup)
	cgroupv2, err := cgroup.IsVersion2(context.TODO(), testclient.DataPlaneClient)
	if err != nil {
		return "", err
	}
	if cgroupv2 {
		fullPath = filepath.Join(cgroupRoot, containerCgroup)
		cpusetMemsPath = filepath.Join(fullPath, "cpuset.mems.effective")
	} else {
		fullPath = filepath.Join(cgroupRoot, "cpuset", containerCgroup)
		cpusetMemsPath = filepath.Join(fullPath, "cpuset.mems")
	}
	cmd = []string{"cat", cpusetMemsPath}
	out, err = nodes.ExecCommand(ctx, targetNode, cmd)
	if err != nil {
		return "", err
	}
	memoryNodes = testutils.ToString(out)
	testlog.Infof("test pod %s with container id %s has Memory nodes %s", testPod.Name, containerID, memoryNodes)
	return memoryNodes, err
}

// CreateHugePagesVolumeMounts create Huge pages volume mounts
func (mm MMPod) CreateHugePagesVolumeMounts() *corev1.VolumeMount {
	if (fmt.Sprintf("hugepages-%si", mm.hpgSize)) == "hugepages-2Mi" {
		return &corev1.VolumeMount{
			Name:      "hugepage-2mi",
			MountPath: "/hugepages-2Mi",
		}
	} else {
		return &corev1.VolumeMount{
			Name:      "hugepage-1gi",
			MountPath: "/hugepages-1Gi",
		}
	}
}
