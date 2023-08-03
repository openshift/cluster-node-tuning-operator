package __performance_kubelet_node_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

var (
	workerRTNode            *corev1.Node
	workerRTNodes           []corev1.Node
	profile, initialProfile *performancev2.PerformanceProfile
	mc                      *machineconfigv1.MachineConfig
	RunningOnSingleNode     bool
	activation_file         string = "/rootfs/var/lib/ovn-ic/etc/enable_dynamic_cpu_affinity"
	performanceMCP          string
)

var s_ = Describe("[performance] Cgroups and affinity", Ordered, func() {
	var onlineCPUSet cpuset.CPUSet

	BeforeAll(func() {
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		workerRTNode = &workerRTNodes[0]

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

	})

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		var err error
		By(fmt.Sprintf("Checking the profile %s with cpus %s", profile.Name, cpuSpecToString(profile.Spec.CPU)))

		Expect(profile.Spec.CPU.Isolated).NotTo(BeNil())
		Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(workerRTNode)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("[rfe_id: 64006][Dynamic OVS Pinning]", Ordered, func() {
		Context("[Performance Profile applied]", func() {
			It("[test_id:64097] Activation file is created", func() {
				cmd := []string{"ls", activation_file}
				for _, node := range workerRTNodes {
					out, err := nodes.ExecCommandOnNode(cmd, &node)
					Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist ", activation_file)
					Expect(out).To(Equal(activation_file))
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
					out, err := nodes.ExecCommandOnNode(cmd, &node)
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
	Describe("Verification of cgroup layout on the worker node", func() {
		It("Verify OVS was placed to its own cgroup", func() {
			By("checking the cgroup process list")
			cmd := []string{"cat", "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/cgroup.procs"}
			ovsSliceTasks, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsSliceTasks).To(Not(BeEmpty()))
		})

		It("Verify OVS slice is configured correctly", func() {
			By("checking the cpuset spans all cpus")
			cmd := []string{"cat", "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/cpuset.cpus"}
			ovsCpuList, err := nodes.ExecCommandOnNode(cmd, workerRTNode)

			ovsCPUSet, err := cpuset.Parse(ovsCpuList)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsCPUSet).To(Equal(onlineCPUSet))

			By("checking the cpuset has balancing disabled")
			cmd = []string{"cat", "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/cpuset.sched_load_balance"}
			ovsLoadBalancing, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsLoadBalancing).To(Equal("0"))
		})
		It("[test_id:64098] ovs-vswitchd and ovsdb-server are under ovs.slice cgroup", func() {
			processCgroups := "cpuset:/ovs.slice"
			pidList, err := getOVSServicesPid(workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			for _, pid := range pidList {
				cgroupPath := fmt.Sprintf("/proc/%s/cgroup", pid)
				checkCgroupCmd := []string{"cat", cgroupPath}
				cgroupInfo, err := nodes.ExecCommandOnNode(checkCgroupCmd, workerRTNode)
				ok, err := regexp.MatchString(processCgroups, cgroupInfo)
				if err != nil {
					testlog.Error(err.Error())
				}
				if ok {
					testlog.Info(processCgroups)
				}
			}
		})
	})

	Describe("Affinity", func() {
		Context("ovn-kubenode Pods affinity ", func() {
			BeforeEach(func() {
				initialProfile = profile.DeepCopy()
			})
			It("[test_id:64100] matches with ovs process affinity", func() {
				ovnPod, err := getOvnPod(workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainersids, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				// Generally there are many containers inside a kubenode pods
				// we don't need to check cpus used by all the containers
				// we take first container
				cpus := getCpusUsedByOvnContainer(ovnContainersids[0])
				testlog.Infof("Cpus used by ovn Containers are %s", cpus)
				pidList, err := getOVSServicesPid(workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				for _, pid := range pidList {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("grep Cpus_allowed_list /proc/%s/status | awk '{print $2}'", pid)}
					cpusOfovsServices, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(cpusOfovsServices).ToNot(BeEmpty(), "unable to fetch cpus of ovs pid %s", pid)
					testlog.Infof("cpus used by ovs services are: %s", cpusOfovsServices)
					Expect(cpus).To(Equal(cpusOfovsServices), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", cpus, cpusOfovsServices)
					if err != nil {
						testlog.Error(err.Error())
					}
				}
			})

			It("[test_id:64101] Creating gu pods modifies affinity of ovs", func() {
				var testpod *corev1.Pod
				var err error
				testpod = pods.GetTestPod()
				testpod.Namespace = testutils.NamespaceTesting
				testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}

				testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod), corev1.PodConditionType(corev1.PodReady), corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod, "test")
				Expect(err).ToNot(HaveOccurred())

				containerCgroup := ""
				Eventually(func() string {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
					containerCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					return containerCgroup
				}, (cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)), 5*time.Second).ShouldNot(BeEmpty())

				By("Get cpus used by the pod")
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
				output, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				testlog.Infof("%v pod is using %v cpus", testpod.Name, output)
				ovnPod, err := getOvnPod(workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainers, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				cpus := getCpusUsedByOvnContainer(ovnContainers[0])
				pidList, err := getOVSServicesPid(workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				for _, pid := range pidList {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("grep Cpus_allowed_list /proc/%s/status | awk '{print $2}'", pid)}
					cpusOfovServices, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(cpus).To(Equal(cpusOfovServices), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", cpus, cpusOfovServices)
					if err != nil {
						testlog.Error(err.Error())
					}
				}
				deleteTestPod(testpod)
			})

			It("[test_id:64102] Create and remove gu pods to verify affinity of ovs are changed appropriately", func() {
				var testpod1, testpod2 *corev1.Pod
				var err error

				// Create testpod1
				testpod1 = pods.GetTestPod()
				testpod1.Namespace = testutils.NamespaceTesting
				testpod1.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}

				testpod1.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod1)
				Expect(err).ToNot(HaveOccurred())
				testpod1, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod1), corev1.PodConditionType(corev1.PodReady), corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod1, "test")
				Expect(err).ToNot(HaveOccurred())

				// Get cpus used by testpod1
				containerCgroup := ""
				Eventually(func() string {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
					containerCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					return containerCgroup
				}, (cluster.ComputeTestTimeout(60*time.Second, RunningOnSingleNode)), 5*time.Second).ShouldNot(BeEmpty())
				By("Get cpu's used by container")
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
				output, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				cpus, err := cpuset.Parse(output)
				testlog.Infof("cpus used by pod %v is %v", testpod1.Name, cpus)

				// Create testpod1
				testpod2 = pods.GetTestPod()
				testpod2.Namespace = testutils.NamespaceTesting
				testpod2.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}
				testpod2.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod2)
				Expect(err).ToNot(HaveOccurred())
				testpod2, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod2), corev1.PodConditionType(corev1.PodReady), corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
				By("Getting the container cpuset.cpus cgroup")
				containerID, err = pods.GetContainerIDByName(testpod2, "test")
				Expect(err).ToNot(HaveOccurred())

				// Get cpus used by testpod2
				containerCgroup = ""
				Eventually(func() string {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
					containerCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					return containerCgroup
				}, (cluster.ComputeTestTimeout(60*time.Second, RunningOnSingleNode)), 5*time.Second).ShouldNot(BeEmpty())

				cmd = []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
				output, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				cpus, err = cpuset.Parse(output)
				testlog.Infof("cpus used by pod %v is %v", testpod2.Name, cpus)

				// Get cpus used by the ovnkubenode-pods containers
				// Each kubenode pods have many containers, we check cpus of only 1 container
				ovnContainerCpus, err := getOvnContainerCpus(workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
				pidList, err := getOVSServicesPid(workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				for _, pid := range pidList {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("grep Cpus_allowed_list /proc/%s/status | awk '{print $2}'", pid)}
					cpusOfovServices, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
					testlog.Infof("cpus used by ovs service %s is %s", pid, cpusOfovServices)
					Expect(cpusOfovServices).To(Equal(ovnContainerCpus), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpusOfovServices)
					if err != nil {
						testlog.Error(err.Error())
					}
				}
				// Delete testpod1
				testlog.Infof("Deleting pod %v", testpod1.Name)
				deleteTestPod(testpod1)

				// Check the cpus of ovnkubenode pods
				ovnContainerCpus, err = getOvnContainerCpus(workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods after deleting pod %v is %v", testpod1.Name, ovnContainerCpus)
				pidList, err = getOVSServicesPid(workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				for _, pid := range pidList {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("grep Cpus_allowed_list /proc/%s/status | awk '{print $2}'", pid)}
					cpusOfovServices, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
					testlog.Infof("cpus used by ovs service %s is %s", pid, cpusOfovServices)
					Expect(cpusOfovServices).To(Equal(ovnContainerCpus), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpusOfovServices)
					if err != nil {
						testlog.Error(err.Error())
					}
				}

				// Delete testpod2
				deleteTestPod(testpod2)
			})

			It("[test_id:64103] ovs process affinity still excludes guaranteed pods after reboot", func() {

				// create a deployment to deploy gu pods
				dp := newDeployment()
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

				// Check the cpus of ovn kube node pods and ovs services
				ovnContainerCpus, err := getOvnContainerCpus(workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
				pidList, err := getOVSServicesPid(workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, pid := range pidList {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("grep Cpus_allowed_list /proc/%s/status | awk '{print $2}'", pid)}
					cpusOfovServices, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
					testlog.Infof("cpus used by ovs service %s is %s", pid, cpusOfovServices)
					Expect(cpusOfovServices).To(Equal(ovnContainerCpus), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpusOfovServices)
					if err != nil {
						testlog.Error(err.Error())
					}
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

				// Check the cpus of ovn kube node pods and ovs services
				ovnContainerCpus, err = getOvnContainerCpus(workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
				pidList, err = getOVSServicesPid(workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				for _, pid := range pidList {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("grep Cpus_allowed_list /proc/%s/status | awk '{print $2}'", pid)}
					cpusOfovServices, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
					testlog.Infof("cpus used by ovs services is %s", cpusOfovServices)
					Expect(cpusOfovServices).To(Equal(ovnContainerCpus), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpusOfovServices)
					if err != nil {
						testlog.Error(err.Error())
					}
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

// deleteTestPod removes guaranteed pod
func deleteTestPod(testpod *corev1.Pod) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return
	}

	err = testclient.Client.Delete(context.TODO(), testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())
}

// getOvnPod Get OVN Kubenode pods running on the worker cnf node
func getOvnPod(workerNode *corev1.Node) (corev1.Pod, error) {
	var ovnKubeNodePod corev1.Pod
	ovnpods := &corev1.PodList{}
	options := &client.ListOptions{
		Namespace: "openshift-ovn-kubernetes",
	}
	err := testclient.Client.List(context.TODO(), ovnpods, options)
	if err != nil {
		return ovnKubeNodePod, err
	}
	for _, pod := range ovnpods.Items {
		if strings.Contains(pod.Name, "node") && pod.Spec.NodeName == workerRTNode.Name {
			ovnKubeNodePod = pod
			break
		}
	}
	testlog.Infof("ovn kube node pod running on %s is %s", workerNode.Name, ovnKubeNodePod.Name)
	return ovnKubeNodePod, err
}

// getOvnPodContainers returns containerids of all containers running inside
// ovn kube node pod
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

// getCpusUsedByOvnContainer returns cpus used by the ovn kube node container
func getCpusUsedByOvnContainer(ovnKubeNodePodCtnid string) string {
	var cpus string
	var err error
	var containerCgroup = ""
	Eventually(func() string {
		cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name '*%s*'", ovnKubeNodePodCtnid)}
		containerCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
		Expect(err).ToNot(HaveOccurred(), "failed to run %s cmd", cmd)
		return containerCgroup
	}, 10*time.Second, 5*time.Second).ShouldNot(BeEmpty())
	cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
	cpus, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
	Expect(err).ToNot(HaveOccurred())

	// Wait for a period of time as it takes some time(10s) for cpu manager
	// to update the cpu usage when gu pod is deleted or created
	time.Sleep(30 * time.Second)
	cmd = []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
	cpus, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
	Expect(err).ToNot(HaveOccurred())
	return cpus
}

// getOvnContain erCpus returns the cpus used by the container inside the ovn kubenode pods
func getOvnContainerCpus(workerRTNode *corev1.Node) (string, error) {
	var ovnContainerCpus string
	ovnPod, err := getOvnPod(workerRTNode)
	if err != nil {
		return "", err
	}
	ovnContainers, err := getOvnPodContainers(&ovnPod)
	if err != nil {
		return "", err
	}
	ovnContainerCpus = getCpusUsedByOvnContainer(ovnContainers[0])
	testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
	return ovnContainerCpus, nil
}

// getOVSServicesPid returns the pid of ovs-vswitchd and ovsdb-server
func getOVSServicesPid(workerNode *corev1.Node) ([]string, error) {
	var pids []string
	ovsServices := []string{"ovs-vswitchd", "ovsdb-server"}
	for _, service := range ovsServices {
		cmd := []string{"pidof", service}
		output, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
		if err != nil {
			return nil, err
		}
		pid := strings.Fields(output)
		pids = append(pids, pid...)
	}
	return pids, nil
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
									corev1.ResourceCPU:    resource.MustParse("1"),
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
