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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/systemd"
)

const minRequiredCPUs = 8

var _ = Describe("[performance] Cgroups and affinity", Ordered, Label(string(label.OVSPinning)), func() {
	const (
		activation_file string = "/rootfs/var/lib/ovn-ic/etc/enable_dynamic_cpu_affinity"
		cgroupRoot      string = "/rootfs/sys/fs/cgroup"
	)
	var (
		onlineCPUSet            cpuset.CPUSet
		workerRTNode            *corev1.Node
		workerRTNodes           []corev1.Node
		profile, initialProfile *performancev2.PerformanceProfile
		poolName                string
		performanceMCP          string
		ovsSliceCgroup          string
		ctx                     context.Context = context.Background()
		ovsSystemdServices      []string
		isCgroupV2              bool
		err                     error
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

		poolName = poolname.GetByProfile(ctx, profile)

		isCgroupV2, err = cgroup.IsVersion2(ctx, testclient.DataPlaneClient)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		ovsSystemdServices = ovsSystemdServicesOnOvsSlice(ctx, workerRTNode)

	})

	BeforeEach(func() {
		By(fmt.Sprintf("Checking the profile %s with cpus %s", profile.Name, cpuSpecToString(profile.Spec.CPU)))

		Expect(profile.Spec.CPU.Isolated).NotTo(BeNil())
		Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(context.TODO(), workerRTNode)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("[rfe_id: 64006][Dynamic OVS Pinning]", Ordered, Label(string(label.Tier0)), func() {
		Context("[Performance Profile applied]", func() {
			It("[test_id:64097] Activation file is created", func() {
				cmd := []string{"ls", activation_file}
				for _, node := range workerRTNodes {
					output, err := nodes.ExecCommand(context.TODO(), &node, cmd)
					Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist ", activation_file)
					out := testutils.ToString(output)
					Expect(out).To(Equal(activation_file))
				}
			})

			It("[test_id:73046] Verify ovn kube node pod have their cpuset.cpus set to all available cpus", func() {
				ovnKubenodepod, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				containerIds, err := ovnPodContainers(&ovnKubenodepod)
				Expect(err).ToNot(HaveOccurred())
				for _, ctn := range containerIds {
					var containerCgroupPath string
					pid, err := nodes.ContainerPid(ctx, workerRTNode, ctn)
					Expect(err).ToNot(HaveOccurred())
					cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pid)}
					out, err := nodes.ExecCommand(context.TODO(), workerRTNode, cmd)
					Expect(err).ToNot(HaveOccurred())
					cgroupPathOfPid, err := cgroup.PidParser(out)
					Expect(err).ToNot(HaveOccurred())
					if isCgroupV2 {
						containerCgroupPath = filepath.Join(cgroupRoot, cgroupPathOfPid)
					} else {
						controller := filepath.Join(cgroupRoot, "/cpuset")
						containerCgroupPath = filepath.Join(controller, cgroupPathOfPid)
					}
					cmd = []string{"cat", filepath.Join(containerCgroupPath, "/cpuset.cpus")}
					out, err = nodes.ExecCommand(ctx, workerRTNode, cmd)
					Expect(err).ToNot(HaveOccurred())
					cpus := testutils.ToString(out)
					containerCpuset, err := cpuset.Parse(cpus)
					Expect(err).ToNot(HaveOccurred())
					Expect(containerCpuset).To(Equal(onlineCPUSet), "Burstable pod containers cpuset.cpus do not match total online cpus")
				}

			})

		})

		Context("[Performance Profile Modified]", Label(string(label.Tier1)), func() {
			BeforeEach(func() {
				initialProfile = profile.DeepCopy()
			})
			It("[test_id:64099] Activation file doesn't get deleted", func() {
				policy := "best-effort"
				// Need to make some changes to pp , causing system reboot
				// and check if activation files is modified or deleted
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
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

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, profile)

				By("Checking Activation file")
				cmd := []string{"ls", activation_file}
				for _, node := range workerRTNodes {
					output, err := nodes.ExecCommand(context.TODO(), &node, cmd)
					Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist ", activation_file)
					out := testutils.ToString(output)
					Expect(out).To(Equal(activation_file))
				}
			})
			AfterEach(func() {
				By("Reverting the Profile")
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				currentSpec, _ := json.Marshal(profile.Spec)
				spec, _ := json.Marshal(initialProfile.Spec)
				if !bytes.Equal(currentSpec, spec) {
					profiles.UpdateWithRetry(initialProfile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)
				}
			})
		})
	})

	Context("Verification of cgroup layout on the worker node", Label(string(label.Tier0)), func() {
		var ctx context.Context = context.TODO()
		var cgroupProcs, cgroupCpusetCpus, cgroupLoadBalance string
		BeforeAll(func() {
			pids, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "unable to fetch pid of ovs services")
			cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pids[0])}
			out, err := nodes.ExecCommand(context.TODO(), workerRTNode, cmd)
			Expect(err).ToNot(HaveOccurred())
			ovsSliceCgroup, err = cgroup.PidParser(out)
			Expect(err).ToNot(HaveOccurred())
			cgroupSlices := strings.Split(ovsSliceCgroup, "/")
			parentCgroup := strings.Join(cgroupSlices[:len(cgroupSlices)-1], "/")
			if isCgroupV2 {
				cgroupProcs = fmt.Sprintf("/rootfs/sys/fs/cgroup/%s/cgroup.procs", ovsSliceCgroup)
				cgroupCpusetCpus = fmt.Sprintf("/rootfs/sys/fs/cgroup/%s/cpuset.cpus.effective", parentCgroup)
			} else {
				cgroupProcs = fmt.Sprintf("%s/cpuset/%s/cgroup.procs", cgroupRoot, parentCgroup)
				cgroupCpusetCpus = fmt.Sprintf("%s/cpuset/%s/cpuset.cpus", cgroupRoot, parentCgroup)
				cgroupLoadBalance = fmt.Sprintf("%s/cpuset/%s/cpuset.sched_load_balance", cgroupRoot, parentCgroup)
			}
		})
		chkOvsCgrpProcs := func(node *corev1.Node) (string, error) {
			testlog.Info("Verify cgroup.procs is not empty")
			cmd := []string{"cat", cgroupProcs}
			out, err := nodes.ExecCommand(context.TODO(), node, cmd)
			if err != nil {
				return "", err
			}
			output := testutils.ToString(out)
			return output, nil
		}
		chkOvsCgrpCpuset := func(node *corev1.Node) (string, error) {
			cmd := []string{"cat", cgroupCpusetCpus}
			out, err := nodes.ExecCommand(context.TODO(), node, cmd)
			if err != nil {
				return "", err
			}
			output := testutils.ToString(out)
			return output, nil
		}
		chkOvsCgroupLoadBalance := func(node *corev1.Node) (string, error) {
			cmd := []string{"cat", cgroupLoadBalance}
			out, err := nodes.ExecCommand(context.TODO(), node, cmd)
			if err != nil {
				return "", err
			}
			output := testutils.ToString(out)
			return output, nil
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

			if isCgroupV2 {
				Skip("CPU load balance can be checked only functionally on cgroupv2")
			}
			result, err = chkOvsCgroupLoadBalance(workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("0"))
		})
	})

	Describe("Affinity", func() {
		var ctx context.Context = context.TODO()
		Context("ovn-kubenode Pods affinity ", Label(string(label.Tier2)), func() {
			It("[test_id:64100] matches with ovs process affinity", func() {
				ovnPod, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainerids, err := ovnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				// Generally there are many containers inside a kubenode pods
				// we don't need to check cpus used by all the containers
				// we take first container
				containerPid, err := nodes.ContainerPid(ctx, workerRTNode, ovnContainerids[0])
				Expect(err).ToNot(HaveOccurred())
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ctnCpuset := taskSet(ctx, containerPid, workerRTNode)
				testlog.Infof("Cpus used by ovn Containers are %s", ctnCpuset.String())
				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				cpumaskList, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList {
					Expect(ctnCpuset).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ctnCpuset.String(), cpumask.String())
				}

			})

			It("[test_id:64101] Creating gu pods modifies affinity of ovs", func() {
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
				err = testclient.DataPlaneClient.Create(ctx, testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				cmd := []string{"taskset", "-pc", "1"}
				outputb, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, "", cmd)
				Expect(err).ToNot(HaveOccurred())
				testpodCpus := bytes.Split(outputb, []byte(":"))
				testlog.Infof("%v pod is using cpus %v", testpod.Name, string(testpodCpus[1]))

				By("Get ovnpods running on the worker cnf node")
				ovnPod, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")

				By("Get cpu used by ovn pod containers")
				// We are fetching the container Process pid and
				// using taskset we are fetching cpus used by the container process
				// instead of using containers cpuset.cpus
				ovnContainers, err := ovnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				containerPid, err := nodes.ContainerPid(ctx, workerRTNode, ovnContainers[0])
				Expect(err).ToNot(HaveOccurred())
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ctnCpuset := taskSet(ctx, containerPid, workerRTNode)
				testlog.Infof("Container of ovn pod %s is using cpus %s", ovnPod.Name, ctnCpuset.String())

				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				cpumaskList, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList {
					Expect(ctnCpuset).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ctnCpuset.String(), cpumask.String())
				}
				deleteTestPod(ctx, testpod)

			})

			It("[test_id:64102] Create and remove gu pods to verify affinity of ovs are changed appropriately", func() {
				var testpod1, testpod2 *corev1.Pod
				var err error
				ovnPod, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				checkCpuCount(ctx, workerRTNode)
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
				err = testclient.DataPlaneClient.Create(ctx, testpod1)
				Expect(err).ToNot(HaveOccurred())
				testpod1, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod1), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				tasksetcmd := []string{"taskset", "-pc", "1"}
				testpod1Cpus, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod1, "", tasksetcmd)
				Expect(err).ToNot(HaveOccurred())
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
				err = testclient.DataPlaneClient.Create(ctx, testpod2)
				Expect(err).ToNot(HaveOccurred())
				testpod2, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod2), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				By("fetch cpus used by container process using taskset")
				testpod2Cpus, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod2, "", tasksetcmd)
				Expect(err).ToNot(HaveOccurred())
				testlog.Infof("%v pod is using %v cpus", testpod2.Name, string(testpod2Cpus))

				// Get cpus used by the ovnkubenode-pods containers
				// Each kubenode pods have many containers, we check cpus of only 1 container
				ovnContainers, err := ovnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				containerPid, err := nodes.ContainerPid(context.TODO(), workerRTNode, ovnContainers[0])
				Expect(err).ToNot(HaveOccurred())
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ovnContainerCpuset1 := taskSet(ctx, containerPid, workerRTNode)
				testlog.Infof("Container of ovn pod %s is using cpus %s", ovnPod.Name, ovnContainerCpuset1.String())
				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// We wait for 30 seconds for ovs process cpu affinity to be updated
				time.Sleep(30 * time.Second)
				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList1, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList1 {
					Expect(ovnContainerCpuset1).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpuset1.String(), cpumask.String())
				}
				// Delete testpod1
				testlog.Infof("Deleting pod %v", testpod1.Name)
				deleteTestPod(context.TODO(), testpod1)

				time.Sleep(30 * time.Second)
				// Check the cpus of ovnkubenode pods
				ovnContainerCpuset2 := taskSet(ctx, containerPid, workerRTNode)
				testlog.Infof("cpus used by ovn kube node pods after deleting pod %v is %v", testpod1.Name, ovnContainerCpuset2.String())
				// we wait some time for ovs process affinity to change
				time.Sleep(30 * time.Second)

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList2, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList2 {
					Expect(ovnContainerCpuset2).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpuset2.String(), cpumask.String())
				}
				// Delete testpod2
				deleteTestPod(context.TODO(), testpod2)
			})

			It("[test_id:64103] ovs process affinity still excludes guaranteed pods after reboot", func() {
				checkCpuCount(context.TODO(), workerRTNode)
				var dp *appsv1.Deployment = newDeployment()
				// create a deployment to deploy gu pods
				testNode := make(map[string]string)
				testNode["kubernetes.io/hostname"] = workerRTNode.Name
				dp.Spec.Template.Spec.NodeSelector = testNode
				err := testclient.DataPlaneClient.Create(ctx, dp)
				Expect(err).ToNot(HaveOccurred(), "Unable to create Deployment")

				defer func() {
					// delete deployment
					testlog.Infof("Deleting Deployment %v", dp.Name)
					err := testclient.DataPlaneClient.Delete(ctx, dp)
					Expect(err).ToNot(HaveOccurred())
					// once deployment is deleted
					// wait till the ovs process affinity is reverted back
					pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					cpumaskList, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
					Expect(err).ToNot(HaveOccurred())					
					Eventually(func() bool {
						for _, cpumask := range cpumaskList {
							testlog.Warningf("ovs services cpu mask is %s instead of %s", cpumask.String(), onlineCPUSet.String())
							// since cpuset.CPUSet contains map in its struct field we can't compare
							// the structs directly. After the deployment is deleted, the cpu mask
							// of ovs services should contain all cpus , which is generally 0-N (where
							// N is total number of cpus, this should be easy to compare.
							if cpumask.String() != onlineCPUSet.String() {
								return false
							}
						}
						return true
					}, 2*time.Minute, 10*time.Second).Should(BeTrue())
				}()

				ovnPod, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainerids, err := ovnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				containerPid, err := nodes.ContainerPid(ctx, workerRTNode, ovnContainerids[0])
				Expect(err).ToNot(HaveOccurred())
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ovnContainerCpuset := taskSet(ctx, containerPid, workerRTNode)
				testlog.Infof("Container of ovn pod %s is using cpus %s", ovnPod.Name, ovnContainerCpuset.String())
				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				//wait for 30 seconds for ovs process to have its cpu affinity updated
				time.Sleep(30 * time.Second)
				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList1, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList1 {
					Expect(ovnContainerCpuset).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpuset.String(), cpumask.String())
				}

				testlog.Info("Rebooting the node")
				rebootCmd := "chroot /rootfs systemctl reboot"
				testlog.TaggedInfof("Reboot", "Node %q: Rebooting", workerRTNode.Name)
				_, _ = nodes.ExecCommand(ctx, workerRTNode, []string{"sh", "-c", rebootCmd})
				testlog.Info("Node Rebooted")

				By("Rebooted node manually and waiting for mcp to get Updating status")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting for mcp to be updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				// After reboot verify test pod created using deployment is running
				// Get pods from the deployment
				listOptions := &client.ListOptions{
					Namespace:     dp.Namespace,
					FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": workerRTNode.Name}),
					LabelSelector: labels.SelectorFromSet(labels.Set{"type": "telco"}),
				}
				podList := &corev1.PodList{}
				dpObj := client.ObjectKeyFromObject(dp)
				Eventually(func() bool {
					if err := testclient.DataPlaneClient.List(context.TODO(), podList, listOptions); err != nil {
						return false
					}
					if err = testclient.DataPlaneClient.Get(context.TODO(), dpObj, dp); err != nil {
						return false
					}
					if dp.Status.ReadyReplicas != int32(2) {
						testlog.Warningf("Waiting for deployment: %q to have %d replicas ready, current number of replicas: %d", dpObj.String(), int32(2), dp.Status.ReadyReplicas)
						return false
					}
					for _, s := range podList.Items[0].Status.ContainerStatuses {
						if !s.Ready {
							return false
						}
					}
					return true
				}, 5*time.Minute, 10*time.Second).Should(BeTrue())
				ovnPodAfterReboot, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainerIdsAfterReboot, err := ovnPodContainers(&ovnPodAfterReboot)
				Expect(err).ToNot(HaveOccurred())
				containerPid, err = nodes.ContainerPid(ctx, workerRTNode, ovnContainerIdsAfterReboot[0])
				Expect(err).ToNot(HaveOccurred())
				// we need to wait as process affinity can change
				time.Sleep(30 * time.Second)
				ovnContainerCpusetAfterReboot := taskSet(ctx, containerPid, workerRTNode)
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpusetAfterReboot.String())
				pidListAfterReboot, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)

				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList2, err := getCPUMaskForPids(ctx, pidListAfterReboot, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList2 {
					Expect(ovnContainerCpusetAfterReboot).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpusetAfterReboot.String(), cpumask.String())
				}
			})

			// Automates OCPBUGS-35347: ovs-vswitchd is using isolated cpu pool instead of reserved pool
			It("[test_id:75257] verify ovs-switchd threads inherit cpu affinity", func() {
				checkCpuCount(context.TODO(), workerRTNode)
				By("Get Thread Affinity of ovs-vswitchd process")
				//Get ovs-switchd thread affinity
				threadAffinity, err := ovsSwitchdThreadAffinity(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify thread affinity contains all the online cpu's
				for _, line := range threadAffinity {
					if line != "" {
						cpumask := strings.Split(line, ":")
						threadsCpuset, err := cpuset.Parse(strings.TrimSpace(cpumask[1]))
						Expect(err).ToNot(HaveOccurred())
						Expect(threadsCpuset).To(Equal(onlineCPUSet))
					}
				}

				// create deployment with 2 replicas and each pod have 2 cpus
				var dp *appsv1.Deployment = newDeployment()
				testNode := make(map[string]string)
				testNode["kubernetes.io/hostname"] = workerRTNode.Name
				dp.Spec.Template.Spec.NodeSelector = testNode
				err = testclient.DataPlaneClient.Create(ctx, dp)
				Expect(err).ToNot(HaveOccurred(), "Unable to create Deployment")

				// Delete deployment
				defer func() {
					By("Delete Deployment")
					testlog.Infof("Deleting Deployment %v", dp.Name)
					err := testclient.DataPlaneClient.Delete(ctx, dp)
					Expect(err).ToNot(HaveOccurred())
					// once deployment is deleted
					// wait till the ovs process affinity is reverted back
					pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					cpumaskList, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					Eventually(func() bool {
						for _, cpumask := range cpumaskList {
							testlog.Warningf("ovs services cpu mask is %s instead of %s", cpumask.String(), onlineCPUSet.String())
							// since cpuset.CPUSet contains map in its struct field we can't compare
							// the structs directly. After the deployment is delete, the cpu mask
							// of ovs services should contain all cpus , which is generally 0-N (where
							// N is total number of cpus, this should be easy to compare.
							if cpumask.String() != onlineCPUSet.String() {
								return false
							}
						}
						return true
					}, 2*time.Minute, 10*time.Second).Should(BeTrue())
				}()

				testlog.Info("Get the pods list form the deployment")
				// Get pods from the deployment
				listOptions := &client.ListOptions{
					Namespace:     dp.Namespace,
					FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": workerRTNode.Name}),
					LabelSelector: labels.SelectorFromSet(labels.Set{"type": "telco"}),
				}
				podList := &corev1.PodList{}
				dpObj := client.ObjectKeyFromObject(dp)
				Eventually(func() bool {
					if err := testclient.DataPlaneClient.List(context.TODO(), podList, listOptions); err != nil {
						return false
					}
					if err = testclient.DataPlaneClient.Get(context.TODO(), dpObj, dp); err != nil {
						return false
					}
					if dp.Status.ReadyReplicas != int32(2) {
						testlog.Warningf("Waiting for deployment: %q to have %d replicas ready, current number of replicas: %d", dpObj.String(), int32(2), dp.Status.ReadyReplicas)
						return false
					}
					for _, s := range podList.Items[0].Status.ContainerStatuses {
						if !s.Ready {
							return false
						}
					}
					return true
				}, 10*time.Second, 5*time.Second).Should(BeTrue())
				testlog.Info("Get ovs-vswitchd threads affinity post deployment")
				postDeploymentThreadAffinity, err := ovsSwitchdThreadAffinity(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, pod := range podList.Items {
					cmd := []string{"taskset", "-pc", "1"}
					outputb, err := pods.ExecCommandOnPod(testclient.K8sClient, &pod, "", cmd)
					Expect(err).ToNot(HaveOccurred())
					testpodCpus := bytes.Split(outputb, []byte(":"))
					testlog.Infof("%v pod is using cpus %v", pod.Name, string(testpodCpus[1]))
					podcpus, err := cpuset.Parse(strings.TrimSpace(string(testpodCpus[1])))
					Expect(err).ToNot(HaveOccurred())
					for _, line := range postDeploymentThreadAffinity {
						if line != "" {
							cpumask := strings.Split(line, ":")
							threadsCpuset, err := cpuset.Parse(strings.TrimSpace(cpumask[1]))
							Expect(err).ToNot(HaveOccurred())
							testlog.Infof("ovs-switchd thread CpuAffinity: %s, pod %s Affinity: %s", threadsCpuset.String(), pod.Name, podcpus.String())
							Expect(podcpus.IsSubsetOf(threadsCpuset)).To(Equal(false))
						}
					}
				}
				testlog.Infof("Deleting one of the pods of the deployment %q", dpObj.String())
				podToDelete := podList.Items[0]
				err = testclient.DataPlaneClient.Get(ctx, client.ObjectKeyFromObject(&podToDelete), &podToDelete)
				Expect(err).ToNot(HaveOccurred())
				err = testclient.DataPlaneClient.Delete(ctx, &podToDelete)
				Expect(err).ToNot(HaveOccurred())
				err = pods.WaitForDeletion(ctx, &podToDelete, pods.DefaultDeletionTimeout*time.Second)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() bool {
					err = testclient.DataPlaneClient.Get(context.TODO(), dpObj, dp)
					if dp.Status.ReadyReplicas != int32(2) {
						testlog.Warningf("Waiting for deployment: %q to have %d replicas ready, current number of replicas: %d", dpObj.String(), int32(2), dp.Status.ReadyReplicas)
						return false
					}
					return true
				}).WithTimeout(time.Minute*5).WithPolling(time.Second*30).Should(BeTrue(), "deployment %q failed to have %d running replicas within the defined period", dpObj.String(), int32(2))

				// Get pods from the deployment
				listOptions = &client.ListOptions{
					Namespace:     dp.Namespace,
					FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": workerRTNode.Name}),
					LabelSelector: labels.SelectorFromSet(labels.Set{"type": "telco"}),
				}
				podList = &corev1.PodList{}
				Eventually(func() bool {
					if err := testclient.DataPlaneClient.List(context.TODO(), podList, listOptions); err != nil {
						return false
					}
					if len(podList.Items) < 1 {
						return false
					}
					for _, s := range podList.Items[0].Status.ContainerStatuses {
						if !s.Ready {
							return false
						}
					}
					return true
				}, 10*time.Second, 5*time.Second).Should(BeTrue())
				// Post deletion of deployment pods verify ovs-vswitchd thread affinity
				refresshedThreadAffinity, err := ovsSwitchdThreadAffinity(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, pod := range podList.Items {
					cmd := []string{"taskset", "-pc", "1"}
					outputb, err := pods.ExecCommandOnPod(testclient.K8sClient, &pod, "", cmd)
					Expect(err).ToNot(HaveOccurred())
					testpodCpus := bytes.Split(outputb, []byte(":"))
					testlog.Infof("%v pod is using cpus %v", pod.Name, string(testpodCpus[1]))
					podcpus, err := cpuset.Parse(strings.TrimSpace(string(testpodCpus[1])))
					Expect(err).ToNot(HaveOccurred())
					for _, line := range refresshedThreadAffinity {
						if line != "" {
							cpumask := strings.Split(line, ":")
							threadsCpuset, err := cpuset.Parse(strings.TrimSpace(cpumask[1]))
							Expect(err).ToNot(HaveOccurred())
							testlog.Infof("ovs-switchd thread CpuAffinity: %s, pod %s Affinity: %s", threadsCpuset.String(), pod.Name, podcpus.String())
							Expect(podcpus.IsSubsetOf(threadsCpuset)).To(Equal(false))
						}
					}
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
	out, err := nodes.ExecCommand(ctx, workerNode, []string{"nproc", "--all"})
	if err != nil {
		Fail(fmt.Sprintf("Failed to fetch online CPUs: %v", err))
	}
	onlineCPUCount := testutils.ToString(out)
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
	err := testclient.DataPlaneClient.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return
	}

	err = testclient.DataPlaneClient.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())
}

// ovnCnfNodePod returns OVN Kubenode pods running on the worker cnf node
func ovnCnfNodePod(ctx context.Context, workerNode *corev1.Node) (corev1.Pod, error) {
	var ovnKubeNodePod corev1.Pod
	ovnpods := &corev1.PodList{}
	options := &client.ListOptions{
		Namespace: "openshift-ovn-kubernetes",
	}
	err := testclient.DataPlaneClient.List(ctx, ovnpods, options)
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

// ovnPodContainers returns containerids of all containers running inside ovn kube node pod
func ovnPodContainers(ovnKubeNodePod *corev1.Pod) ([]string, error) {
	var ovnKubeNodePodContainerids []string
	var errRet error
	for _, ovnctn := range ovnKubeNodePod.Spec.Containers {
		ctnName, err := pods.GetContainerIDByName(ovnKubeNodePod, ovnctn.Name)
		if err != nil {
			err = fmt.Errorf("unable to fetch container id of %v", ovnctn)
			errRet = err
		}
		ovnKubeNodePodContainerids = append(ovnKubeNodePodContainerids, ctnName)
	}
	return ovnKubeNodePodContainerids, errRet
}

// getCPUMaskForPids returns cpu mask of ovs process pids
func getCPUMaskForPids(ctx context.Context, pidList []string, targetNode *corev1.Node) ([]cpuset.CPUSet, error) {
	var cpumaskList []cpuset.CPUSet

	for _, pid := range pidList {
		cmd := []string{"taskset", "-pc", pid}
		out, err := nodes.ExecCommand(ctx, targetNode, cmd)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch cpus of %s: %s", pid, err)
		}
		cpumask := testutils.ToString(out)

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

// ovsSystemdServicesOnOvsSlice returns the systemd services dependent on ovs.slice cgroup
func ovsSystemdServicesOnOvsSlice(ctx context.Context, workerRTNode *corev1.Node) []string {
	ovsServices, err := systemd.ShowProperty(context.TODO(), "ovs.slice", "RequiredBy", workerRTNode)
	Expect(err).ToNot(HaveOccurred())
	serviceList := strings.Split(strings.TrimSpace(ovsServices), "=")
	ovsSystemdServices := strings.Split(serviceList[1], " ")
	Expect(len(ovsSystemdServices)).ToNot(Equal(0), "OVS Services not moved under ovs.slice cgroup")
	return ovsSystemdServices
}

// ovsPids Returns Pid of ovs services running inside the ovs.slice cgroup
func ovsPids(ctx context.Context, ovsSystemdServices []string, workerRTNode *corev1.Node) ([]string, error) {
	var pidList []string
	var errRet error
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
		if errRet != nil {
			errRet = err
		}
		ovsPid := strings.Split(strings.TrimSpace(pid), "=")
		pidList = append(pidList, ovsPid[1])
	}
	return pidList, errRet
}

// taskSet returns cpus used by the pid
func taskSet(ctx context.Context, pid string, workerRTNode *corev1.Node) cpuset.CPUSet {
	cmd := []string{"taskset", "-pc", pid}
	out, err := nodes.ExecCommand(ctx, workerRTNode, cmd)
	Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus using taskset")
	output := testutils.ToString(out)
	tasksetOutput := strings.Split(strings.TrimSpace(output), ":")
	cpus := strings.TrimSpace(tasksetOutput[1])
	ctnCpuset, err := cpuset.Parse(cpus)
	Expect(err).ToNot(HaveOccurred(), "Unable to parse %s", cpus)
	return ctnCpuset
}

// ovsSwitchdThreadAffinity returns cpu affinity of ovs-vswitchd threads
func ovsSwitchdThreadAffinity(ctx context.Context, workerRTNode *corev1.Node) ([]string, error) {
	cmd := []string{"pidof", "ovs-vswitchd"}
	switchdPidB, err := nodes.ExecCommand(ctx, workerRTNode, cmd)
	if err != nil {
		return nil, fmt.Errorf("unable to get pid of ovs-vswitchd process")
	}
	ovsswitchdPid := strings.TrimSpace(string(switchdPidB))
	affinity := []string{"taskset", "-apc", ovsswitchdPid}
	out, err := nodes.ExecCommand(ctx, workerRTNode, affinity)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch thread affinity of ovs-vswitchd process")
	}
	threadAffinity := strings.Split(string(out), "\n")
	return threadAffinity, nil
}
