package __performance_kubelet_node_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/systemd"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const minRequiredCPUs = 8

type ContainerInfo struct {
	PID int `json:"pid"`
}

var _ = Describe("[performance] Cgroups and affinity", Ordered, func() {
	const (
		activation_file string = "/rootfs/var/lib/ovn-ic/etc/enable_dynamic_cpu_affinity"
		cgroupRoot      string = "/rootfs/sys/fs/cgroup"
	)
	var (
		onlineCPUSet            cpuset.CPUSet
		workerRTNode            *corev1.Node
		workerRTNodes           []corev1.Node
		profile, initialProfile *performancev2.PerformanceProfile
		performanceMCP          string
		ovsSliceCgroup          string
		ctx                     context.Context = context.Background()
		ovsSystemdServices      []string
	)

	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		workerRTNode = &workerRTNodes[0]

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		ovsSystemdServices = ovsSystemdServicesOnOvsSlice(ctx, workerRTNode)

	})

	BeforeEach(func() {
		var err error
		By(fmt.Sprintf("Checking the profile %s with cpus %s", profile.Name, cpuSpecToString(profile.Spec.CPU)))

		Expect(profile.Spec.CPU.Isolated).NotTo(BeNil())
		Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(context.TODO(), workerRTNode)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("[rfe_id: 64006][Dynamic OVS Pinning]", Ordered, func() {
		Context("[Performance Profile applied]", func() {
			It("[test_id:64097] Activation file is created", func() {
				cmd := []string{"ls", activation_file}
				for _, node := range workerRTNodes {
					out, err := nodes.ExecCommandOnNode(context.TODO(), cmd, &node)
					Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist ", activation_file)
					Expect(out).To(Equal(activation_file))
				}
			})

			It("Verify ovn kube node pod have their cpuset.cpus set to all available cpus", func() {
				ovnKubenodepod, err := OvnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				containerIds, err := getOvnPodContainers(&ovnKubenodepod)
				for _, ctn := range containerIds {
					var containerCgroupPath string
					pid := getContainerProcess(ctx, workerRTNode, ctn)
					cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%d/cgroup", pid)}
					out, err := nodes.ExecCommandOnMachineConfigDaemon(context.TODO(), workerRTNode, cmd)
					Expect(err).ToNot(HaveOccurred())
					controllers := bytes.Split(out, []byte("\n"))
					if len(controllers) > 2 {
						controller := filepath.Join(cgroupRoot, "/cpuset")
						containerCgroupPath = filepath.Join(controller, cgroupParser(out))
					} else {
						containerCgroupPath = filepath.Join(cgroupRoot, cgroupParser(out))
					}
					cmd = []string{"cat", fmt.Sprintf("%s", filepath.Join(containerCgroupPath, "/cpuset.cpus"))}
					cpus, err := nodes.ExecCommandOnNode(ctx, cmd, workerRTNode)
					containerCpuset, err := cpuset.Parse(cpus)
					Expect(containerCpuset).To(Equal(onlineCPUSet), "Burstable pod containers cpuset.cpus do not match total online cpus")
				}
			})
			It("check all pods of ovnkube-node have the right affinity", func() {
				ovnKubenodepod, err := OvnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				containerIds, err := getOvnPodContainers(&ovnKubenodepod)
				for _, ctn := range containerIds {
					pid := getContainerProcess(ctx, workerRTNode, ctn)
					ctnCpuset := taskSet(ctx, strconv.Itoa(pid), workerRTNode)
					reservedCpuset, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(ctnCpuset).ToNot(Equal(reservedCpuset))
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})

		Context("[Performance Profile Modified]", func() {
			BeforeEach(func() {
				initialProfile = profile.DeepCopy()
			})
			It("[test_id:64099] Activation file doesn't get deleted", func() {
				performanceMCP, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())
				policy := "best-effort"
				// Need to make some changes to pp , causing system reboot
				// and check if activation files is modified or deleted
				profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch latest performance profile")
				currentPolicy := profile.Spec.NUMA.TopologyPolicy
				if *currentPolicy == "best-effort" {
					policy = "restricted"
				}
				profile.Spec.NUMA = &performancev2.NUMA{
					TopologyPolicy: &policy,
				}
				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)
				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				By("Checking Activation file")
				cmd := []string{"ls", activation_file}
				for _, node := range workerRTNodes {
					out, err := nodes.ExecCommandOnNode(context.TODO(), cmd, &node)
					Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist ", activation_file)
					Expect(out).To(Equal(activation_file))
				}
			})
			AfterEach(func() {
				By("Reverting the Profile")
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				currentSpec, _ := json.Marshal(profile.Spec)
				spec, _ := json.Marshal(initialProfile.Spec)
				performanceMCP, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())
				// revert only if the profile changes.
				if !bytes.Equal(currentSpec, spec) {
					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
			})
		})
	})

	Context("Verification of cgroup layout on the worker node", func() {
		var ctx context.Context = context.TODO()
		var totalNumberofCgroupMembership int
		var cgroupProcs, cgroupCpusetCpus, cgroupLoadBalance string
		BeforeAll(func() {
			pids, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "unable to fetch pid of ovs services")
			cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pids[0])}
			out, err := nodes.ExecCommandOnMachineConfigDaemon(context.TODO(), workerRTNode, cmd)
			Expect(err).ToNot(HaveOccurred())
			controllers := bytes.Split(out, []byte("\n"))
			if len(controllers) == 0 {
				testlog.Errorf("Unable to fetch cgroup membership of %s pid", pids[0])
			}
			totalNumberofCgroupMembership = len(controllers)
			ovsSliceCgroup = cgroupParser(out)
			fmt.Println("full path = ", ovsSliceCgroup)
			cgroupSlices := strings.Split(ovsSliceCgroup, "/")
			parentCgroup := strings.Join(cgroupSlices[:len(cgroupSlices)-1], "/")

			if len(controllers) > 2 {
				cgroupProcs = fmt.Sprintf("/rootfs/sys/fs/cgroup/cpuset/%s/cgroup.procs", parentCgroup)
				cgroupCpusetCpus = fmt.Sprintf("/rootfs/sys/fs/cgroup/cpuset/%s/cpuset.cpus", parentCgroup)
				cgroupLoadBalance = fmt.Sprintf("/rootfs/sys/fs/cgroup/cpuset/%s/cpuset.sched_load_balance", parentCgroup)
			} else {
				cgroupProcs = fmt.Sprintf("/rootfs/sys/fs/cgroup/%s/cgroup.procs", ovsSliceCgroup)
				cgroupCpusetCpus = fmt.Sprintf("/rootfs/sys/fs/cgroup/%s/cpuset.cpus.effective", parentCgroup)
			}
		})

		chkOvsCgrpProcs := func(node *corev1.Node) (string, error) {
			testlog.Info("Verify cgroup.procs is not empty")
			cmd := []string{"cat", cgroupProcs}
			fmt.Println("cmd = ", cmd)
			return nodes.ExecCommandOnNode(context.TODO(), cmd, node)
		}
		chkOvsCgrpCpuset := func(node *corev1.Node) (string, error) {
			cmd := []string{"cat", cgroupCpusetCpus}
			return nodes.ExecCommandOnNode(context.TODO(), cmd, node)
		}
		chkOvsCgroupLoadBalance := func(node *corev1.Node) (string, error) {
			cmd := []string{"cat", cgroupLoadBalance}
			return nodes.ExecCommandOnNode(context.TODO(), cmd, node)
		}

		It("[test_id:64098] Verify cgroup layout on worker node", func() {
			// check cgroups.procs
			result, err := chkOvsCgrpProcs(workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Not(BeEmpty()))

			result, err = chkOvsCgrpCpuset(workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "Unable to read cpuset.cpus")
			ovsCPUSet, err := cpuset.Parse(result)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsCPUSet).To(Equal(onlineCPUSet))

			if totalNumberofCgroupMembership == 2 {
				Skip("CPU load balance can be checked only functionally on cgroupv2")
			}
			result, err = chkOvsCgroupLoadBalance(workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("0"))
		})
	})

	Describe("Affinity", func() {
		var ctx context.Context = context.TODO()
		Context("ovn-kubenode Pods affinity ", func() {
			testutils.CustomBeforeAll(func() {
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				initialProfile = profile.DeepCopy()
			})
			It("[test_id:64100] matches with ovs process affinity", func() {
				testutils.KnownIssueJira("OCPBUGS-30806")
				ovnPod, err := OvnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")

				reservedCPU := string(*profile.Spec.CPU.Reserved)
				reservedCPUSet, err := cpuset.Parse(reservedCPU)
				Expect(err).ToNot(HaveOccurred())

				/*podCgrop := "kubepods-burstable-pod" + ovnPod.UID
				fmt.Println(podCgrop)*/
				ovnContainerids, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())

				// Generally there are many containers inside a kubenode pods
				// we don't need to check cpus used by all the containers
				// we take first container
				containerPid := getContainerProcess(context.TODO(), workerRTNode, ovnContainerids[0])
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ctnCpuset := taskSet(ctx, strconv.Itoa(containerPid), workerRTNode)
				testlog.Infof("Cpus used by ovn Containers are %s", ctnCpuset.String())
				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				cpumaskList, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList {
					Expect(ctnCpuset).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ctnCpuset.String(), cpumask.String())
					// The cpu affinity of pods and ovs services can match
					// but we wanted to make sure that their affinity is not that of reserve cpus
					Expect(cpumask).ToNot(Equal(reservedCPUSet), "ovs services are having reserved cpus %s", reservedCPU)
				}
			})

			It("[test_id:64101] Creating gu pods modifies affinity of ovs", func() {
				testutils.KnownIssueJira("OCPBUGS-30806")
				var testpod *corev1.Pod
				var err error
				testpod = pods.GetTestPod()
				testpod.Namespace = testutils.NamespaceTesting
				testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				}
				testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				cmd := []string{"taskset", "-pc", "1"}
				outputb, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, "", cmd)
				testpodCpus := bytes.Split(outputb, []byte(":"))
				testlog.Infof("%v pod is using cpus %v", testpod.Name, string(testpodCpus[1]))

				By("Get ovnpods running on the worker cnf node")
				ovnPod, err := OvnCnfNodePod(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")

				By("Get cpu used by ovn pod containers")
				// We are fetching the container Process pid and
				// using taskset we are fetching cpus used by the container process
				// instead of using containers cpuset.cpus
				ovnContainers, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				containerPid := getContainerProcess(context.TODO(), workerRTNode, ovnContainers[0])
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ctnCpuset := taskSet(ctx, strconv.Itoa(containerPid), workerRTNode)
				testlog.Infof("Container of ovn pod %s is using cpus %s", ovnPod.Name, ctnCpuset.String())

				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				cpumaskList, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList {
					Expect(ctnCpuset).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ctnCpuset.String(), cpumask.String())
				}
				deleteTestPod(context.TODO(), testpod)
			})

			It("[test_id:64102] Create and remove gu pods to verify affinity of ovs are changed appropriately", func() {
				var testpod1, testpod2 *corev1.Pod
				var err error
				ovnPod, err := OvnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				checkCpuCount(context.TODO(), workerRTNode)
				// Create testpod1
				testpod1 = pods.GetTestPod()
				testpod1.Namespace = testutils.NamespaceTesting
				testpod1.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}

				testpod1.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod1)
				Expect(err).ToNot(HaveOccurred())
				testpod1, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod1), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				tasksetcmd := []string{"taskset", "-pc", "1"}
				testpod1Cpus, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod1, "", tasksetcmd)
				testlog.Infof("%v pod is using %v cpus", testpod1.Name, string(testpod1Cpus))

				// Create testpod2
				testpod2 = pods.GetTestPod()
				testpod2.Namespace = testutils.NamespaceTesting
				testpod2.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}
				testpod2.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod2)
				Expect(err).ToNot(HaveOccurred())
				testpod2, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod2), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				By("fetch cpus used by container process using taskset")
				testpod2Cpus, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod2, "", tasksetcmd)
				testlog.Infof("%v pod is using %v cpus", testpod2.Name, string(testpod2Cpus))

				// Get cpus used by the ovnkubenode-pods containers
				// Each kubenode pods have many containers, we check cpus of only 1 container
				ovnContainers, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				containerPid := getContainerProcess(context.TODO(), workerRTNode, ovnContainers[0])
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ovnContainerCpuset1 := taskSet(ctx, strconv.Itoa(containerPid), workerRTNode)
				testlog.Infof("Container of ovn pod %s is using cpus %s", ovnPod.Name, ovnContainerCpuset1.String())
				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// We wait for 30 seconds for ovs process cpu affinity to be updated
				time.Sleep(30 * time.Second)
				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList1, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList1 {
					Expect(ovnContainerCpuset1).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpuset1.String(), cpumask.String())
				}
				// Delete testpod1
				testlog.Infof("Deleting pod %v", testpod1.Name)
				deleteTestPod(context.TODO(), testpod1)

				time.Sleep(30 * time.Second)
				// Check the cpus of ovnkubenode pods
				ovnContainerCpuset2 := taskSet(ctx, strconv.Itoa(containerPid), workerRTNode)
				testlog.Infof("cpus used by ovn kube node pods after deleting pod %v is %v", testpod1.Name, ovnContainerCpuset2.String())
				// we wait some time for ovs process affinity to change
				time.Sleep(30 * time.Second)

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList2, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList2 {
					Expect(ovnContainerCpuset2).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpuset2.String(), cpumask.String())
				}
				// Delete testpod2
				deleteTestPod(context.TODO(), testpod2)
			})

			It("[test_id:64103] ovs process affinity still excludes guaranteed pods after reboot", func() {
				checkCpuCount(context.TODO(), workerRTNode)
				var dp *appsv1.Deployment
				// create a deployment to deploy gu pods
				dp = newDeployment()
				testNode := make(map[string]string)
				testNode["kubernetes.io/hostname"] = workerRTNode.Name
				dp.Spec.Template.Spec.NodeSelector = testNode
				err := testclient.Client.Create(context.TODO(), dp)
				Expect(err).ToNot(HaveOccurred(), "Unable to create Deployment")

				defer func() {
					// delete deployment
					testlog.Infof("Deleting Deployment %v", dp.Name)
					err := testclient.Client.Delete(context.TODO(), dp)
					Expect(err).ToNot(HaveOccurred())
				}()
				ovnPod, err := OvnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainerids, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				containerPid := getContainerProcess(context.TODO(), workerRTNode, ovnContainerids[0])
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ovnContainerCpuset := taskSet(ctx, strconv.Itoa(containerPid), workerRTNode)
				testlog.Infof("Container of ovn pod %s is using cpus %s", ovnPod.Name, ovnContainerCpuset.String())
				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				//wait for 30 seconds for ovs process to have its cpu affinity updated
				time.Sleep(30 * time.Second)
				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList1, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList1 {
					Expect(ovnContainerCpuset).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpuset.String(), cpumask.String())
				}

				testlog.Info("Rebooting the node")
				// reboot the node, for that we change the numa policy to best-effort
				// Note: this is used only to trigger reboot
				policy := "best-effort"
				// Need to make some changes to pp , causing system reboot
				// and check if activation files is modified or deleted
				profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch latest performance profile")
				currentPolicy := profile.Spec.NUMA.TopologyPolicy
				if *currentPolicy == "best-effort" {
					policy = "restricted"
				}
				profile.Spec.NUMA = &performancev2.NUMA{
					TopologyPolicy: &policy,
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)
				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				// After reboot we want the deployment to be ready before moving forward
				desiredStatus := appsv1.DeploymentStatus{
					Replicas:          3,
					AvailableReplicas: 3,
				}

				err = waitForCondition(dp, desiredStatus)
				ovnPodAfterReboot, err := OvnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainerIdsAfterReboot, err := getOvnPodContainers(&ovnPodAfterReboot)
				Expect(err).ToNot(HaveOccurred())
				containerPid = getContainerProcess(context.TODO(), workerRTNode, ovnContainerIdsAfterReboot[0])
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ovnContainerCpusetAfterReboot := taskSet(ctx, strconv.Itoa(containerPid), workerRTNode)
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpusetAfterReboot.String())
				pidListAfterReboot, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)

				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList2, err := getCPUMaskForPids(context.TODO(), pidListAfterReboot, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList2 {
					Expect(ovnContainerCpusetAfterReboot).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpusetAfterReboot.String(), cpumask.String())
				}
			})

			AfterAll(func() {
				By("Reverting the Profile")
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				currentSpec, _ := json.Marshal(profile.Spec)
				spec, _ := json.Marshal(initialProfile.Spec)
				performanceMCP, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())
				// revert only if the profile changes.
				if !bytes.Equal(currentSpec, spec) {
					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
			})
		})

	})
})

func cpuSpecToString(cpus *performancev2.CPU) string {
	if cpus == nil {
		return "<nil>"
	}
	sb := strings.Builder{}
	if cpus.Reserved != nil {
		fmt.Fprintf(&sb, "reserved=[%s]", *cpus.Reserved)
	}
	if cpus.Isolated != nil {
		fmt.Fprintf(&sb, " isolated=[%s]", *cpus.Isolated)
	}
	if cpus.BalanceIsolated != nil {
		fmt.Fprintf(&sb, " balanceIsolated=%t", *cpus.BalanceIsolated)
	}
	return sb.String()
}

// checkCpuCount check if the node has sufficient cpus
func checkCpuCount(ctx context.Context, workerNode *corev1.Node) {
	onlineCPUCount, err := nodes.ExecCommandOnNode(ctx, []string{"nproc", "--all"}, workerNode)
	if err != nil {
		Fail(fmt.Sprintf("Failed to fetch online CPUs: %v", err))
	}
	onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
	if err != nil {
		Fail(fmt.Sprintf("failed to convert online CPU count to integer: %v", err))
	}
	if onlineCPUInt <= minRequiredCPUs {
		Skip(fmt.Sprintf("This test requires more than %d isolated CPUs, current available CPUs: %s", minRequiredCPUs, onlineCPUCount))
	}
}

// deleteTestPod removes guaranteed pod
func deleteTestPod(ctx context.Context, testpod *corev1.Pod) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.Client.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return
	}

	err = testclient.Client.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())
}

// OvnCnfPod Get OVN Kubenode pods running on the worker cnf node
func OvnCnfNodePod(ctx context.Context, workerNode *corev1.Node) (corev1.Pod, error) {
	var ovnKubeNodePod corev1.Pod
	ovnpods := &corev1.PodList{}
	options := &client.ListOptions{
		Namespace: "openshift-ovn-kubernetes",
	}

	err := testclient.Client.List(ctx, ovnpods, options)
	if err != nil {
		return ovnKubeNodePod, err
	}
	for _, pod := range ovnpods.Items {
		if strings.Contains(pod.Name, "node") && pod.Spec.NodeName == workerNode.Name {
			ovnKubeNodePod = pod
			break
		}
	}
	testlog.Infof("ovn kube node pod running on %s is %s", workerNode.Name, ovnKubeNodePod.Name)
	return ovnKubeNodePod, err
}

// getOvnPodContainers returns containerids of all containers running inside ovn kube node pod
func getOvnPodContainers(ovnKubeNodePod *corev1.Pod) ([]string, error) {
	var ovnKubeNodePodContainerids []string
	var err error
	for _, ovnctn := range ovnKubeNodePod.Spec.Containers {
		ctnName, err := pods.GetContainerIDByName(ovnKubeNodePod, ovnctn.Name)
		if err != nil {
			err = fmt.Errorf("unable to fetch container id of %v", ovnctn)
		}
		ovnKubeNodePodContainerids = append(ovnKubeNodePodContainerids, ctnName)
	}
	return ovnKubeNodePodContainerids, err
}

// getCPUMaskForPids returns a slice containing cpu affinity of ovs services
func getCPUMaskForPids(ctx context.Context, pidList []string, targetNode *corev1.Node) ([]cpuset.CPUSet, error) {
	var cpumaskList []cpuset.CPUSet

	for _, pid := range pidList {
		cmd := []string{"taskset", "-pc", pid}
		cpumask, err := nodes.ExecCommandOnNode(ctx, cmd, targetNode)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch cpus of %s: %s", pid, err)
		}
		mask := strings.SplitAfter(cpumask, " ")
		maskSet, err := cpuset.Parse(mask[len(mask)-1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse cpuset: %s", err)
		}
		cpumaskList = append(cpumaskList, maskSet)
	}

	return cpumaskList, nil
}

func newDeployment() *appsv1.Deployment {
	var replicas int32 = 2
	dp := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-",
			Labels: map[string]string{
				"testDeployment": "",
			},
			Namespace: testutils.NamespaceTesting,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"type": "telco",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"type": "telco",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test",
							Image:   images.Test(),
							Command: []string{"sleep", "inf"},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("200Mi"),
									corev1.ResourceCPU:    resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
	}
	return dp
}

// waitForCondition wait for deployment to be ready
func waitForCondition(deployment *appsv1.Deployment, status appsv1.DeploymentStatus) error {
	var err error
	var val bool
	err = wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err != nil {
			if errors.IsNotFound(err) {
				return false, fmt.Errorf("deployment not found")
			}
			return false, err
		}
		val = deployment.Status.Replicas == status.Replicas && deployment.Status.AvailableReplicas == status.AvailableReplicas
		return val, err
	})

	return err
}

func ovsSystemdServicesOnOvsSlice(ctx context.Context, workerRTNode *corev1.Node) []string {
	ovsServices, err := systemd.ShowProperty(context.TODO(), "ovs.slice", "RequiredBy", workerRTNode)
	Expect(err).ToNot(HaveOccurred())
	serviceList := strings.Split(strings.TrimSpace(ovsServices), "=")
	ovsSystemdServices := strings.Split(serviceList[1], " ")
	Expect(len(ovsSystemdServices)).ToNot(Equal(0), "OVS Services not moved under ovs.slice cgroup")
	return ovsSystemdServices
}

func ovsPids(ctx context.Context, ovsSystemdServices []string, workerRTNode *corev1.Node) ([]string, error) {
	var pidList []string
	var err error
	for _, service := range ovsSystemdServices {
		//we need to ignore oneshot services which are part of ovs.slices
		serviceType, err := systemd.ShowProperty(ctx, service, "Type", workerRTNode)
		if err != nil {
			return nil, err
		}
		if strings.Contains(serviceType, "oneshot") {
			continue
		}
		pid, err := systemd.ShowProperty(context.TODO(), service, "ExecMainPID", workerRTNode)
		ovsPid := strings.Split(strings.TrimSpace(pid), "=")
		pidList = append(pidList, ovsPid[1])
	}
	return pidList, err
}

// parsing /proc/pid/cgroup
func cgroupParser(lines []byte) string {
	var cgroupPath string
	for _, line := range bytes.Split(lines, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		fields := bytes.Split(line, []byte(":"))
		if len(fields) != 3 {
			fmt.Printf("Error parsing cgroup: expected 3 fields but got %d:\n", len(fields))
		}

		if bytes.Contains(fields[1], []byte("=")) {
			continue
		}
		path := string(fields[2])
		if len(path) > len(cgroupPath) {
			cgroupPath = path
		}
	}
	return strings.TrimSpace(cgroupPath)
}

// get cpus used by container
func getContainerProcess(ctx context.Context, workerRTNode *corev1.Node, ovnKubeNodePodCtnid string) int {
	var err error
	var stateInfo = []byte{}
	Eventually(func() []byte {
		// TODO: This path is not compatible with cgroupv2.
		cmd := []string{"/bin/bash", "-c", fmt.Sprintf("chroot /rootfs runc --log-format json state %s", ovnKubeNodePodCtnid)}
		stateInfo, err = nodes.ExecCommandOnMachineConfigDaemon(ctx, workerRTNode, cmd)
		Expect(err).ToNot(HaveOccurred(), "failed to run %s cmd", cmd)
		return stateInfo
	}, 10*time.Second, 5*time.Second).ShouldNot(BeEmpty())
	var containerInfo ContainerInfo
	err = json.Unmarshal(stateInfo, &containerInfo)
	if err != nil {
		fmt.Println("Error unmarshalling JSON:", err)
		return 0
	}
	return containerInfo.PID
}

func taskSet(ctx context.Context, pid string, workerRTNode *corev1.Node) cpuset.CPUSet {
	cmd := []string{"taskset", "-pc", pid}
	output, err := nodes.ExecCommandOnNode(ctx, cmd, workerRTNode)
	Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus using taskset")
	tasksetOutput := strings.Split(strings.TrimSpace(output), ":")
	cpus := strings.TrimSpace(tasksetOutput[1])
	ctnCpuset, err := cpuset.Parse(cpus)
	Expect(err).ToNot(HaveOccurred(), "Unable to parse %s", cpus)
	return ctnCpuset
}
