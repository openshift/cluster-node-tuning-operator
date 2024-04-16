package __performance

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

var _ = Describe("[performance]Hugepages", Ordered, func() {
	const cgroupRoot string = "/rootfs/sys/fs/cgroup/"
	var (
		workerRTNode *corev1.Node
		profile      *performancev2.PerformanceProfile
		ctx          context.Context = context.Background()
		cgroupV2     bool
	)

	testutils.CustomBeforeAll(func() {
		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO
		cgroupV2, err = cgroup.IsVersion2(ctx, testclient.Client)
		Expect(err).ToNot(HaveOccurred())

	})

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		var err error
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		Expect(workerRTNodes).ToNot(BeEmpty())
		workerRTNode = &workerRTNodes[0]

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		if profile.Spec.HugePages == nil || len(profile.Spec.HugePages.Pages) == 0 {
			Skip("Hugepages is not configured in performance profile")
		}
	})

	// We have multiple hugepages e2e tests under the upstream, so the only thing that we should check, if the PAO configure
	// correctly number of hugepages that will be available on the node
	Context("[rfe_id:27369]when NUMA node specified", func() {
		It("[test_id:27752][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] should be allocated on the specifed NUMA node", func() {
			for _, page := range profile.Spec.HugePages.Pages {
				if page.Node == nil {
					continue
				}

				hugepagesSize, err := machineconfig.GetHugepagesSizeKilobytes(page.Size)
				Expect(err).ToNot(HaveOccurred())

				availableHugepagesFile := fmt.Sprintf("/sys/devices/system/node/node%d/hugepages/hugepages-%skB/nr_hugepages", *page.Node, hugepagesSize)
				nrHugepages := checkHugepagesStatus(context.TODO(), availableHugepagesFile, workerRTNode)

				freeHugepagesFile := fmt.Sprintf("/sys/devices/system/node/node%d/hugepages/hugepages-%skB/free_hugepages", *page.Node, hugepagesSize)
				freeHugepages := checkHugepagesStatus(context.TODO(), freeHugepagesFile, workerRTNode)

				Expect(int32(nrHugepages)).To(Equal(page.Count), "The number of available hugepages should be equal to the number in performance profile")
				Expect(nrHugepages).To(Equal(freeHugepages), "On idle system the number of available hugepages should be equal to free hugepages")
			}
		})
	})

	Context("with multiple sizes", func() {
		It("[test_id:34080] should be supported and available for the container usage", func() {
			for _, page := range profile.Spec.HugePages.Pages {
				hugepagesSize, err := machineconfig.GetHugepagesSizeKilobytes(page.Size)
				Expect(err).ToNot(HaveOccurred())

				availableHugepagesFile := fmt.Sprintf("/sys/kernel/mm/hugepages/hugepages-%skB/nr_hugepages", hugepagesSize)
				if page.Node != nil {
					availableHugepagesFile = fmt.Sprintf("/sys/devices/system/node/node%d/hugepages/hugepages-%skB/nr_hugepages", *page.Node, hugepagesSize)
				}
				nrHugepages := checkHugepagesStatus(context.TODO(), availableHugepagesFile, workerRTNode)

				if discovery.Enabled() && nrHugepages != 0 {
					Skip("Skipping test since other guests might reside in the cluster affecting results")
				}

				freeHugepagesFile := fmt.Sprintf("/sys/kernel/mm/hugepages/hugepages-%skB/free_hugepages", hugepagesSize)
				if page.Node != nil {
					freeHugepagesFile = fmt.Sprintf("/sys/devices/system/node/node%d/hugepages/hugepages-%skB/free_hugepages", *page.Node, hugepagesSize)
				}

				freeHugepages := checkHugepagesStatus(context.TODO(), freeHugepagesFile, workerRTNode)

				Expect(int32(nrHugepages)).To(Equal(page.Count), "The number of available hugepages should be equal to the number in performance profile")
				Expect(nrHugepages).To(Equal(freeHugepages), "On idle system the number of available hugepages should be equal to free hugepages")
			}
		})
	})

	Context("[rfe_id:27354]Huge pages support for container workloads", func() {
		var testpod *corev1.Pod

		AfterEach(func() {
			err := testclient.Client.Delete(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			err = pods.WaitForDeletion(context.TODO(), testpod, pods.DefaultDeletionTimeout*time.Second)
			Expect(err).ToNot(HaveOccurred())
		})

		It("[test_id:27477][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Huge pages support for container workloads", func() {
			hpSize := profile.Spec.HugePages.Pages[0].Size
			var systemCgroup, hugepageInterface string
			hpSizeKb, err := machineconfig.GetHugepagesSizeKilobytes(hpSize)
			Expect(err).ToNot(HaveOccurred())

			By("checking hugepages usage in bytes - should be 0 on idle system")
			if cgroupV2 {
				//we need to check kubepods.slice
				systemCgroup = filepath.Join(cgroupRoot, "kubepods.slice")
				hugepageInterface = fmt.Sprintf("hugetlb.%sB.current", hpSize)
			} else {
				systemCgroup = filepath.Join(cgroupRoot, "hugetlb")
				hugepageInterface = fmt.Sprintf("hugetlb.%sB.usage_in_bytes", hpSize)
			}
			usageHugepagesFile := fmt.Sprintf("%s/%s", systemCgroup, hugepageInterface)
			usageHugepages := checkHugepagesStatus(context.TODO(), usageHugepagesFile, workerRTNode)
			if discovery.Enabled() && usageHugepages != 0 {
				Skip("Skipping test since other guests might reside in the cluster affecting results")
			}
			Expect(usageHugepages).To(Equal(0), "Found used hugepages, expected 0")

			By("running the POD and waiting while it's installing testing tools")
			testpod = getCentosPod(workerRTNode.Name)
			testpod.Namespace = testutils.NamespaceTesting
			testpod.Spec.Containers[0].Resources.Limits = map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceName(fmt.Sprintf("hugepages-%si", hpSize)): resource.MustParse(fmt.Sprintf("%si", hpSize)),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			}
			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())
			testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			Expect(err).ToNot(HaveOccurred())

			cmd2 := []string{"/bin/bash", "-c", "tmux new -d 'LD_PRELOAD=libhugetlbfs.so HUGETLB_MORECORE=yes top -b > /dev/null'"}
			_, err = pods.ExecCommandOnPod(testclient.K8sClient, testpod, "", cmd2)
			Expect(err).ToNot(HaveOccurred())

			By("checking free hugepages - one should be used by pod")
			availableHugepagesFile := fmt.Sprintf("/sys/kernel/mm/hugepages/hugepages-%skB/nr_hugepages", hpSizeKb)
			availableHugepages := checkHugepagesStatus(context.TODO(), availableHugepagesFile, workerRTNode)

			freeHugepagesFile := fmt.Sprintf("/sys/kernel/mm/hugepages/hugepages-%skB/free_hugepages", hpSizeKb)
			Eventually(func() int {
				freeHugepages := checkHugepagesStatus(context.TODO(), freeHugepagesFile, workerRTNode)
				return availableHugepages - freeHugepages
			}, cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode), time.Second).Should(Equal(1))

			By("checking hugepages usage in bytes")
			usageHugepages = checkHugepagesStatus(context.TODO(), usageHugepagesFile, workerRTNode)
			Expect(strconv.Itoa(usageHugepages/1024)).To(Equal(hpSizeKb), "usage in bytes should be %s", hpSizeKb)
		})
	})
})

func checkHugepagesStatus(ctx context.Context, path string, workerRTNode *corev1.Node) int {
	command := []string{"cat", path}
	out, err := nodes.ExecCommand(ctx, workerRTNode, command)
	Expect(err).ToNot(HaveOccurred())
	n, err := strconv.Atoi(strings.Trim(string(out), "\n\r"))
	Expect(err).ToNot(HaveOccurred())
	return n
}

func getCentosPod(nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-hugepages-",
			Labels: map[string]string{
				"test": "",
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "hugepages",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumHugePages},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "test",
					Image:   images.Test(),
					Command: []string{"sleep", "10h"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "hugepages",
							MountPath: "/dev/hugepages",
						},
					},
				},
			},
			NodeSelector: map[string]string{
				testutils.LabelHostname: nodeName,
			},
		},
	}
}
