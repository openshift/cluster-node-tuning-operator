package __performance_kubelet_node_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/baseload"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/systemd"
)

const (
	minRequiredCPUs = 8
	cpuSetReserved  = "reserved"
	cpuSetIsolated  = "isolated"
	cpuSetShared    = "shared"
	cpuSetOfflined  = "offlined"
	cpuSetGUPod     = "guPod"
)

var _ = Describe("[performance] Cgroups and affinity", Ordered, Label(string(label.OVSPinning)), func() {
	const (
		activation_file string = "/rootfs/var/lib/ovn-ic/etc/enable_dynamic_cpu_affinity"
		cgroupRoot      string = "/rootfs/sys/fs/cgroup"
	)
	var (
		reservedCPUSet                cpuset.CPUSet
		isolatedCPUSet                cpuset.CPUSet
		workerRTNode                  *corev1.Node
		workerRTNodes                 []corev1.Node
		profile                       *performancev2.PerformanceProfile
		ovsSliceCgroup                string
		ctx                           context.Context = context.Background()
		ovsSystemdServices            []string
		isCgroupV2                    bool
		isWorkloadPartitioningEnabled bool
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

		if _, ok := workerRTNode.Labels["node-role.kubernetes.io/control-plane"]; ok {
			isSchedulable, err := cluster.IsControlPlaneSchedulable(ctx)
			Expect(err).ToNot(HaveOccurred(), "Unable to check if control plane is schedulable")
			if !isSchedulable {
				Skip("workerRTNode is a control plane node but masters are not schedulable")
			}
		}

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		isCgroupV2, err = cgroup.IsVersion2(ctx, testclient.DataPlaneClient)
		Expect(err).ToNot(HaveOccurred())

		ovsSystemdServices = ovsSystemdServicesOnOvsSlice(ctx, workerRTNode)

		isWorkloadPartitioningEnabled, err = cluster.IsWorkloadPartitioningEnabled(ctx, testclient.Client)
		Expect(err).ToNot(HaveOccurred(), "Unable to check if workload partitioning is enabled")
		testlog.Infof("Workload partitioning enabled: %v", isWorkloadPartitioningEnabled)

		profileCPUSets := parseProfileCPUSets(profile)
		reservedCPUSet = profileCPUSets[cpuSetReserved]
		isolatedCPUSet = profileCPUSets[cpuSetIsolated]
		testlog.Infof("Reserved CPUSet: %s", reservedCPUSet)
		testlog.Infof("Isolated CPUSet: %s", isolatedCPUSet)
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
					if isWorkloadPartitioningEnabled {
						Expect(containerCpuset.Equals(reservedCPUSet)).To(BeTrue(),
							"Under workload partitioning, OVN pod cpuset.cpus should match reserved cpus, got %s expected %s", containerCpuset, reservedCPUSet)
					} else {
						onlineCPUSet, err := nodes.GetOnlineCPUsSet(context.TODO(), workerRTNode)
						Expect(err).ToNot(HaveOccurred())
						Expect(containerCpuset.Equals(onlineCPUSet)).To(BeTrue(),
							"Burstable pod containers cpuset.cpus do not match total online cpus, got %s expected %s", containerCpuset, onlineCPUSet)
					}
				}

			})

		})

		Context("[Node Reboot]", Label(string(label.Tier1)), func() {
			It("[test_id:64099] Activation file doesn't get deleted", func() {
				By(fmt.Sprintf("Rebooting the worker node %q", workerRTNode.Name))
				_, _ = nodes.ExecCommand(ctx, workerRTNode, []string{"sh", "-c", "chroot /rootfs systemctl reboot"})
				nodes.WaitForNotReadyOrFail("Reboot", workerRTNode.Name, 10*time.Minute, 30*time.Second)
				nodes.WaitForReadyOrFail("Reboot", workerRTNode.Name, 10*time.Minute, 30*time.Second)

				By("Checking Activation file")
				cmd := []string{"ls", activation_file}
				output, err := nodes.ExecCommand(context.TODO(), workerRTNode, cmd)
				Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist", activation_file)
				out := testutils.ToString(output)
				Expect(out).To(Equal(activation_file))
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

			ovsCPUSetRaw, err := chkOvsCgrpCpuset(workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			ovsCPUSet, err := cpuset.Parse(ovsCPUSetRaw)
			Expect(err).ToNot(HaveOccurred())
			onlineCPUSet, err := nodes.GetOnlineCPUsSet(context.TODO(), workerRTNode)
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
				By("Collecting OVN container and OVS process affinities")
				ovnAffinity := getOvnContainerAffinity(ctx, workerRTNode)
				ovsAffinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)

				By("Verifying OVS affinity matches expected")
				verifyOvsMatchesExpected(ovnAffinity, ovsAffinities,
					isWorkloadPartitioningEnabled, map[string]cpuset.CPUSet{
						cpuSetReserved: reservedCPUSet,
						cpuSetIsolated: isolatedCPUSet,
						cpuSetGUPod:    cpuset.New(),
					})
			})

			It("[test_id:64101] Creating gu pods modifies affinity of ovs", func() {
				By("Creating a guaranteed pod on the worker node")
				testpod := createGuPod(ctx, workerRTNode)

				By("Collecting OVN container and OVS process affinities")
				ovnAffinity := getOvnContainerAffinity(ctx, workerRTNode)
				ovsAffinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)

				By("Verifying OVS affinity excludes guaranteed pod CPUs")
				guPodCPUs := getGuPodCPUs(ctx, testpod)
				verifyOvsMatchesExpected(ovnAffinity, ovsAffinities,
					isWorkloadPartitioningEnabled, map[string]cpuset.CPUSet{
						cpuSetReserved: reservedCPUSet,
						cpuSetIsolated: isolatedCPUSet,
						cpuSetGUPod:    guPodCPUs,
					})

				Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, testpod)).To(Succeed())
			})

			It("[test_id:64102] Create and remove gu pods to verify affinity of ovs are changed appropriately", func() {
				checkCpuCount(ctx, workerRTNode)

				By("Creating two guaranteed pods on the worker node")
				testpod1 := createGuPod(ctx, workerRTNode)
				testpod2 := createGuPod(ctx, workerRTNode)

				By("Collecting affinities with both GU pods running")
				ovnAffinity := getOvnContainerAffinity(ctx, workerRTNode)
				ovsAffinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)

				By("Verifying OVS affinity excludes both GU pods' CPUs")
				bothGUPodCPUs := getGuPodCPUs(ctx, testpod1).Union(getGuPodCPUs(ctx, testpod2))
				verifyOvsMatchesExpected(ovnAffinity, ovsAffinities,
					isWorkloadPartitioningEnabled, map[string]cpuset.CPUSet{
						cpuSetReserved: reservedCPUSet,
						cpuSetIsolated: isolatedCPUSet,
						cpuSetGUPod:    bothGUPodCPUs,
					})

				By("Deleting first GU pod and verifying OVS affinity adjusts")
				Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, testpod1)).To(Succeed())
				ovnAffinityAfterDelete := getOvnContainerAffinity(ctx, workerRTNode)
				ovsAffinities = getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
				verifyOvsMatchesExpected(ovnAffinityAfterDelete, ovsAffinities,
					isWorkloadPartitioningEnabled, map[string]cpuset.CPUSet{
						cpuSetReserved: reservedCPUSet,
						cpuSetIsolated: isolatedCPUSet,
						cpuSetGUPod:    getGuPodCPUs(ctx, testpod2),
					})

				By("Cleaning up second GU pod")
				Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, testpod2)).To(Succeed())
			})

			It("[test_id:64103] ovs process affinity still excludes guaranteed pods after reboot", func() {
				checkCpuCount(ctx, workerRTNode)

				By("Creating a deployment with guaranteed pods on the worker node")
				dp := newDeployment()
				dp.Spec.Template.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": workerRTNode.Name}
				Expect(testclient.DataPlaneClient.Create(ctx, dp)).To(Succeed(), "Unable to create Deployment")

				defer func() {
					testlog.Infof("Deleting Deployment %s from Namespace %s", dp.Name, dp.Namespace)
					Expect(testclient.DataPlaneClient.Delete(ctx, dp)).To(Succeed())
					baselineCpus := reservedCPUSet.Union(isolatedCPUSet)
					Eventually(func() bool {
						affinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
						for pid, mask := range affinities {
							if !mask.Equals(baselineCpus) {
								testlog.Warningf("OVS pid %s mask is %s instead of %s", pid, mask, baselineCpus)
								return false
							}
						}
						return true
					}, 5*time.Minute, 10*time.Second).Should(BeTrue())
				}()

				dpListOpts := &client.ListOptions{
					Namespace:     dp.Namespace,
					FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": workerRTNode.Name}),
					LabelSelector: labels.SelectorFromSet(labels.Set{"type": "telco"}),
				}

				collectAndVerify := func(phase string) {
					ovnAffinity := getOvnContainerAffinity(ctx, workerRTNode)
					ovsAffinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
					guPodCPUs := collectGuCPUsFromPodList(ctx, dpListOpts)
					testlog.Infof("Phase %s: GU CPUs = %s", phase, guPodCPUs)
					verifyOvsMatchesExpected(ovnAffinity, ovsAffinities,
						isWorkloadPartitioningEnabled, map[string]cpuset.CPUSet{
							cpuSetReserved: reservedCPUSet,
							cpuSetIsolated: isolatedCPUSet,
							cpuSetGUPod:    guPodCPUs,
						})
				}

				waitForDeploymentReady(ctx, dp, dpListOpts, 2)

				By("Verifying affinities before reboot")
				collectAndVerify("before-reboot")

				By(fmt.Sprintf("Rebooting the worker node %q", workerRTNode.Name))
				_, _ = nodes.ExecCommand(ctx, workerRTNode, []string{"sh", "-c", "chroot /rootfs systemctl reboot"})
				nodes.WaitForNotReadyOrFail("Reboot", workerRTNode.Name, 10*time.Minute, 30*time.Second)
				nodes.WaitForReadyOrFail("Reboot", workerRTNode.Name, 10*time.Minute, 30*time.Second)

				By("Waiting for deployment pods to be ready after reboot")
				waitForDeploymentReady(ctx, dp, dpListOpts, 2)

				By("Verifying affinities after reboot")
				collectAndVerify("after-reboot")
			})

			// In this test setup ROLE_WORKER_CNF can target control-plane nodes
			// (for example ROLE_WORKER_CNF=masters). Keep the baseline aligned with
			// the selected profile by validating OVS affinity against
			// reserved+isolated from that profile.
			It("[test_id: 89066] Verify OVS affinity is not restricted to reserved CPUs after control plane node reboot", func() {
				isSchedulable, err := cluster.IsControlPlaneSchedulable(ctx)
				Expect(err).ToNot(HaveOccurred(), "Unable to check if control plane is schedulable")
				if !isSchedulable {
					Skip("Control plane nodes are not schedulable")
				}

				By("Finding a control plane node with OVS dynamic pinning")
				var cpNode *corev1.Node

				if _, ok := workerRTNode.Labels["node-role.kubernetes.io/control-plane"]; ok {
					cpNode = workerRTNode
				} else {
					cpNodes, err := nodes.GetByLabels(map[string]string{"node-role.kubernetes.io/control-plane": ""})
					Expect(err).ToNot(HaveOccurred())
					for i := range cpNodes {
						cmd := []string{"ls", activation_file}
						_, err := nodes.ExecCommand(ctx, &cpNodes[i], cmd)
						if err == nil {
							cpNode = &cpNodes[i]
							break
						}
					}
				}

				if cpNode == nil {
					Skip("No control plane node with OVS dynamic pinning found")
				}
				testlog.Infof("Using control plane node: %s", cpNode.Name)

				cpOvsServices := ovsSystemdServicesOnOvsSlice(ctx, cpNode)
				baselineCPUs := reservedCPUSet.Union(isolatedCPUSet)

				By("Verify OVS affinity before reboot matches profile reserved+isolated")
				ovsBeforeReboot := getOvsAffinities(ctx, cpOvsServices, cpNode)
				Expect(ovsBeforeReboot).ToNot(BeEmpty(), "Expected non-empty OVS affinities on control-plane node before reboot")
				verifyOvsAffinity(ovsBeforeReboot, baselineCPUs)

				By(fmt.Sprintf("Rebooting the control plane node %q", cpNode.Name))
				_, _ = nodes.ExecCommand(ctx, cpNode, []string{"sh", "-c", "chroot /rootfs systemctl reboot"})
				nodes.WaitForNotReadyOrFail("Reboot", cpNode.Name, 10*time.Minute, 30*time.Second)
				nodes.WaitForReadyOrFail("Reboot", cpNode.Name, 10*time.Minute, 30*time.Second)

				By("Waiting for OVN pod to be ready on the control plane node")
				Eventually(func() error {
					_, err := ovnCnfNodePod(ctx, cpNode)
					return err
				}, 5*time.Minute, 10*time.Second).Should(Succeed(),
					"OVN pod did not become ready on control plane node after reboot")

				By("Verify OVS affinity after reboot matches profile reserved+isolated")
				ovsAfterReboot := getOvsAffinities(ctx, cpOvsServices, cpNode)
				Expect(ovsAfterReboot).ToNot(BeEmpty(), "Expected non-empty OVS affinities on control-plane node after reboot")
				verifyOvsAffinity(ovsAfterReboot, baselineCPUs)
			})

			// Automates OCPBUGS-35347: ovs-vswitchd is using isolated cpu pool instead of reserved pool
			It("[test_id:75257] verify ovs-switchd threads inherit cpu affinity", func() {
				checkCpuCount(ctx, workerRTNode)

				By("Verifying ovs-vswitchd thread affinity covers reserved+isolated CPUs")
				threadAffinity, err := ovsSwitchdThreadAffinity(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				baselineCpus := reservedCPUSet.Union(isolatedCPUSet)
				for _, line := range threadAffinity {
					if line != "" {
						parts := strings.Split(line, ":")
						threadsCpuset, err := cpuset.Parse(strings.TrimSpace(parts[1]))
						Expect(err).ToNot(HaveOccurred())
						Expect(threadsCpuset.Equals(baselineCpus)).To(BeTrue(),
							"actual cpuset %s not equals to expected cpuset %s", threadsCpuset, baselineCpus)
					}
				}

				By("Creating a deployment with 2 guaranteed replicas")
				dp := newDeployment()
				dp.Spec.Template.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": workerRTNode.Name}
				Expect(testclient.DataPlaneClient.Create(ctx, dp)).To(Succeed(), "Unable to create Deployment")

				defer func() {
					testlog.Infof("Deleting Deployment %s", dp.Name)
					Expect(testclient.DataPlaneClient.Delete(ctx, dp)).To(Succeed())
					baselineCpus := reservedCPUSet.Union(isolatedCPUSet)
					Eventually(func() bool {
						affinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
						for pid, mask := range affinities {
							if !mask.Equals(baselineCpus) {
								testlog.Warningf("OVS pid %s mask is %s instead of %s", pid, mask, baselineCpus)
								return false
							}
						}
						return true
					}, 2*time.Minute, 10*time.Second).Should(BeTrue())
				}()

				listOpts := &client.ListOptions{
					Namespace:     dp.Namespace,
					FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": workerRTNode.Name}),
					LabelSelector: labels.SelectorFromSet(labels.Set{"type": "telco"}),
				}
				podList := waitForDeploymentReady(ctx, dp, listOpts, 2)

				By("Verifying GU pod CPUs are not a subset of ovs-vswitchd thread affinity")
				postDeployThreadAffinity, err := ovsSwitchdThreadAffinity(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for i := range podList.Items {
					podcpus := getGuPodCPUs(ctx, &podList.Items[i])
					for _, line := range postDeployThreadAffinity {
						if line != "" {
							parts := strings.Split(line, ":")
							threadsCpuset, err := cpuset.Parse(strings.TrimSpace(parts[1]))
							Expect(err).ToNot(HaveOccurred())
							testlog.Infof("ovs-vswitchd thread affinity: %s, pod %s affinity: %s", threadsCpuset, podList.Items[i].Name, podcpus)
							Expect(podcpus.IsSubsetOf(threadsCpuset)).To(BeFalse())
						}
					}
				}

				By("Deleting one pod and waiting for replacement")
				podToDelete := podList.Items[0]
				Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, &podToDelete)).To(Succeed())
				podList = waitForDeploymentReady(ctx, dp, listOpts, 2)

				By("Verifying GU pod CPUs are still not a subset of ovs-vswitchd thread affinity")
				refreshedThreadAffinity, err := ovsSwitchdThreadAffinity(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for i := range podList.Items {
					podcpus := getGuPodCPUs(ctx, &podList.Items[i])
					for _, line := range refreshedThreadAffinity {
						if line != "" {
							parts := strings.Split(line, ":")
							threadsCpuset, err := cpuset.Parse(strings.TrimSpace(parts[1]))
							Expect(err).ToNot(HaveOccurred())
							testlog.Infof("ovs-vswitchd thread affinity: %s, pod %s affinity: %s", threadsCpuset, podList.Items[i].Name, podcpus)
							Expect(podcpus.IsSubsetOf(threadsCpuset)).To(BeFalse())
						}
					}
				}
			})
		})

		Context("Workload Partitioning OVS affinity", Label(string(label.Tier2)), func() {
			BeforeEach(func() {
				if !isWorkloadPartitioningEnabled {
					Skip("Workload partitioning is not enabled on this cluster")
				}
			})

			It("[test_id:89062] Verify OVN pod is restricted to reserved CPUs under workload partitioning", func() {
				ovnPod, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				containerIds, err := ovnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())

				By("Verify each OVN container's cgroup cpuset and process affinity are restricted to reserved CPUs")
				for _, ctn := range containerIds {
					pid, err := nodes.ContainerPid(ctx, workerRTNode, ctn)
					Expect(err).ToNot(HaveOccurred())

					ctnAffinity := taskSet(ctx, pid, workerRTNode)
					testlog.Infof("OVN container pid %s affinity: %s", pid, ctnAffinity)
					Expect(ctnAffinity).To(Equal(reservedCPUSet),
						"Under workload partitioning, OVN container pid %s affinity (%s) should equal reserved CPUs (%s)",
						pid, ctnAffinity, reservedCPUSet)
				}
			})

			It("[test_id:89063] Verify OVS affinity is wider than OVN pod under workload partitioning", func() {
				By("Get OVN container affinity")
				ovnPod, err := ovnCnfNodePod(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
				ovnContainerids, err := ovnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				containerPid, err := nodes.ContainerPid(ctx, workerRTNode, ovnContainerids[0])
				Expect(err).ToNot(HaveOccurred())
				var ctnCpuset cpuset.CPUSet
				Eventually(func() cpuset.CPUSet {
					ctnCpuset = taskSet(ctx, containerPid, workerRTNode)
					return ctnCpuset
				}, 2*time.Minute, 5*time.Second).Should(Equal(reservedCPUSet),
					"OVN container should be restricted to reserved CPUs under workload partitioning")
				testlog.Infof("OVN container affinity: %s", ctnCpuset)

				By("Get OVS process affinity and verify it is wider")
				pidList, err := ovsPids(ctx, ovsSystemdServices, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				pidToCPUs, err := getCPUMaskForPids(ctx, pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				for pid, ovsAffinity := range pidToCPUs {
					testlog.Infof("OVS service pid %s affinity: %s", pid, ovsAffinity)
					Expect(reservedCPUSet.IsSubsetOf(ovsAffinity)).To(BeTrue(),
						"Reserved CPUs (%s) should be a subset of OVS affinity (%s)", reservedCPUSet, ovsAffinity)
					Expect(ovsAffinity.Equals(reservedCPUSet)).To(BeFalse(),
						"OVS affinity (%s) should be wider than reserved CPUs (%s) when no GU pods exist", ovsAffinity, reservedCPUSet)
				}
			})

			It("[test_id:89064] Verify reserved CPUs are always included in OVS affinity under workload partitioning", func() {
				checkCpuCount(ctx, workerRTNode)

				By("Verify reserved CPUs are part of OVS affinity before creating GU pods")
				ovsAffinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
				for pid, mask := range ovsAffinities {
					Expect(reservedCPUSet.IsSubsetOf(mask)).To(BeTrue(),
						"Reserved CPUs (%s) should be a subset of OVS affinity (%s) for pid %s", reservedCPUSet, mask, pid)
				}

				By("Create a GU pod and verify reserved CPUs remain in OVS affinity")
				testpod := createGuPod(ctx, workerRTNode)
				guPodCPUs := getGuPodCPUs(ctx, testpod)

				ovsAffinities = getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
				for pid, mask := range ovsAffinities {
					testlog.Infof("OVS pid %s affinity with GU pod: %s", pid, mask)
					Expect(reservedCPUSet.IsSubsetOf(mask)).To(BeTrue(),
						"Reserved CPUs (%s) should still be a subset of OVS affinity (%s) for pid %s", reservedCPUSet, mask, pid)
					Expect(guPodCPUs.IsSubsetOf(mask)).To(BeFalse(),
						"GU pod CPUs (%s) should NOT be a subset of OVS affinity (%s)", guPodCPUs, mask)
				}

				By("Delete the GU pod and verify reserved CPUs are still in OVS affinity")
				Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, testpod)).To(Succeed())

				baselineCpus := reservedCPUSet.Union(isolatedCPUSet)
				ovsAffinities = getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
				for pid, mask := range ovsAffinities {
					testlog.Infof("OVS pid %s affinity after GU pod deletion: %s", pid, mask)
					Expect(reservedCPUSet.IsSubsetOf(mask)).To(BeTrue(),
						"Reserved CPUs (%s) should be a subset of OVS affinity (%s) for pid %s after deletion", reservedCPUSet, mask, pid)
					Expect(mask).To(Equal(baselineCpus),
						"OVS affinity should return to reserved+isolated after GU pod deletion")
				}
			})

			It("[test_id:89065] Verify OVS affinity excludes CPUs pinned by guaranteed pods under workload partitioning", func() {
				checkCpuCount(ctx, workerRTNode)

				isolatedCPUs := isolatedCPUSet
				isolatedCount := isolatedCPUs.Size()
				if isolatedCount < 2 {
					Skip("Not enough isolated CPUs to run this test")
				}

				nodeLoad, err := baseload.ForNode(ctx, testclient.DataPlaneClient, workerRTNode.Name)
				Expect(err).ToNot(HaveOccurred())
				availableCPUs := nodeLoad.AvailableCPUs(isolatedCount)
				availableCPUs = availableCPUs &^ 1 // round down to even to satisfy SMT alignment
				testlog.Infof("Isolated CPUs: %d, already consumed: %d, available (even-aligned): %d",
					isolatedCount, nodeLoad.CPURequestedCores(), availableCPUs)
				if availableCPUs < 2 {
					Skip(fmt.Sprintf("Not enough available isolated CPUs: %d available out of %d total", availableCPUs, isolatedCount))
				}

				By(fmt.Sprintf("Creating GU pods to consume %d available isolated CPUs", availableCPUs))
				var guPods []*corev1.Pod
				remainingIsolated := availableCPUs
				for remainingIsolated > 0 {
					cpuRequest := remainingIsolated
					if cpuRequest > 2 {
						cpuRequest = 2
					}
					testpod := pods.GetTestPod()
					testpod.Namespace = testutils.NamespaceTesting
					testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", cpuRequest)),
							corev1.ResourceMemory: resource.MustParse("200Mi"),
						},
					}
					testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
					err := testclient.DataPlaneClient.Create(ctx, testpod)
					Expect(err).ToNot(HaveOccurred())
					testpod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
					pods.DumpStateOnFailure(ctx, testclient.K8sClient, testpod, err)
					Expect(err).ToNot(HaveOccurred())
					Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
					guPods = append(guPods, testpod)
					remainingIsolated -= cpuRequest
				}

				defer func() {
					for _, p := range guPods {
						testlog.Infof("Cleaning up GU pod %s", p.Name)
						Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, p)).To(Succeed())
					}
					baselineCpus := reservedCPUSet.Union(isolatedCPUSet)
					Eventually(func() bool {
						affinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
						for pid, mask := range affinities {
							if !mask.Equals(baselineCpus) {
								testlog.Warningf("OVS pid %s mask is %s instead of %s", pid, mask, baselineCpus)
								return false
							}
						}
						return true
					}, 5*time.Minute, 10*time.Second).Should(BeTrue())
				}()

				guPodCPUs := cpuset.New()
				for _, p := range guPods {
					guPodCPUs = guPodCPUs.Union(getGuPodCPUs(ctx, p))
				}
				expected := expectedOvsAffinity(reservedCPUSet, isolatedCPUSet, guPodCPUs)
				testlog.Infof("GU pod CPUs: %s, expected OVS affinity: %s", guPodCPUs, expected)

				By("Verify OVS affinity excludes the guaranteed pods' pinned CPUs")
				ovsAffinities := getOvsAffinities(ctx, ovsSystemdServices, workerRTNode)
				for pid, mask := range ovsAffinities {
					testlog.Infof("OVS pid %s affinity: %s (expected: %s)", pid, mask, expected)
					Expect(mask.Equals(expected)).To(BeTrue(),
						"OVS pid %s affinity (%s) should equal expected (%s) after GU pod pinning",
						pid, mask, expected)
				}
			})
		})

	})
})

// checkCpuCount check if the node has sufficient cpus
func checkCpuCount(ctx context.Context, workerNode *corev1.Node) {
	GinkgoHelper()
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
func getCPUMaskForPids(ctx context.Context, pidList []string, targetNode *corev1.Node) (map[string]cpuset.CPUSet, error) {
	pidToCPUSet := make(map[string]cpuset.CPUSet, len(pidList))
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
		pidToCPUSet[pid] = maskSet
	}

	return pidToCPUSet, nil
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
	GinkgoHelper()
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
	GinkgoHelper()
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

// expectedOvsAffinity computes the expected OVN/OVS CPU affinity set.
// Formula: (reserved + isolated) - GU_Pinned
// reserved+isolated is the profile-derived baseline for OVS. Subtracting
// GU-pinned CPUs yields the expected affinity when GU pods are running.
func expectedOvsAffinity(reservedCPUs, isolatedCPUs, guPodCPUs cpuset.CPUSet) cpuset.CPUSet {
	return reservedCPUs.Union(isolatedCPUs).Difference(guPodCPUs)
}

// getOvnContainerAffinity returns the CPU affinity of the first OVN container
// on the given node, waiting 30s for affinity to stabilise.
func getOvnContainerAffinity(ctx context.Context, node *corev1.Node) cpuset.CPUSet {
	GinkgoHelper()
	ovnPod, err := ovnCnfNodePod(ctx, node)
	Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")
	containerIds, err := ovnPodContainers(&ovnPod)
	Expect(err).ToNot(HaveOccurred())
	pid, err := nodes.ContainerPid(ctx, node, containerIds[0])
	Expect(err).ToNot(HaveOccurred())
	time.Sleep(30 * time.Second)
	affinity := taskSet(ctx, pid, node)
	testlog.Infof("OVN container %s affinity: %s", ovnPod.Name, affinity)
	return affinity
}

// getOvsAffinities returns a pid-to-cpuset map for all OVS service processes
// on the given node, waiting 30s for affinity to stabilise.
func getOvsAffinities(ctx context.Context, services []string, node *corev1.Node) map[string]cpuset.CPUSet {
	GinkgoHelper()
	pidList, err := ovsPids(ctx, services, node)
	Expect(err).ToNot(HaveOccurred())
	time.Sleep(30 * time.Second)
	affinities, err := getCPUMaskForPids(ctx, pidList, node)
	Expect(err).ToNot(HaveOccurred())
	return affinities
}

// verifyOvsAffinity asserts that every OVS process has the given expected CPU set.
func verifyOvsAffinity(ovsAffinities map[string]cpuset.CPUSet, expected cpuset.CPUSet) {
	GinkgoHelper()
	for pid, mask := range ovsAffinities {
		testlog.Infof("OVS pid %s affinity: %s (expected: %s)", pid, mask, expected)
		Expect(mask).To(Equal(expected))
	}
}

// verifyOvsMatchesExpected validates OVS affinity in both workload-partitioning
// and non-workload-partitioning modes.
func verifyOvsMatchesExpected(ovnAffinity cpuset.CPUSet,
	ovsAffinities map[string]cpuset.CPUSet,
	isWorkloadPartitioningEnabled bool,
	expectedCPUSets map[string]cpuset.CPUSet) {
	GinkgoHelper()
	reservedCPUs, ok := expectedCPUSets[cpuSetReserved]
	Expect(ok).To(BeTrue(), "expected CPU map should include %q", cpuSetReserved)
	isolatedCPUs, ok := expectedCPUSets[cpuSetIsolated]
	Expect(ok).To(BeTrue(), "expected CPU map should include %q", cpuSetIsolated)
	guPodCPUs, ok := expectedCPUSets[cpuSetGUPod]
	Expect(ok).To(BeTrue(), "expected CPU map should include %q", cpuSetGUPod)

	if !isWorkloadPartitioningEnabled {
		expectedAffinity := expectedOvsAffinity(reservedCPUs, isolatedCPUs, guPodCPUs)
		Expect(ovnAffinity).To(Equal(expectedAffinity),
			"Without WP, OVN container should be restricted to reserved+isolated CPUs excluding GU pod CPUs")
		verifyOvsAffinity(ovsAffinities, expectedAffinity)
		return
	}
	Expect(ovnAffinity).To(Equal(reservedCPUs),
		"Under WP, OVN container should be restricted to reserved cpus")
	verifyOvsAffinity(ovsAffinities, expectedOvsAffinity(reservedCPUs, isolatedCPUs, guPodCPUs))
}

func parseProfileCPUSets(profile *performancev2.PerformanceProfile) map[string]cpuset.CPUSet {
	GinkgoHelper()
	cpuSets := map[string]cpuset.CPUSet{
		cpuSetReserved: cpuset.New(),
		cpuSetIsolated: cpuset.New(),
		cpuSetShared:   cpuset.New(),
		cpuSetOfflined: cpuset.New(),
	}

	parseCPUSet := func(name string, raw *performancev2.CPUSet) {
		if raw == nil {
			return
		}
		parsed, err := cpuset.Parse(string(*raw))
		Expect(err).ToNot(HaveOccurred(), "Failed to parse %s CPUs from profile", name)
		cpuSets[name] = parsed
	}

	parseCPUSet(cpuSetReserved, profile.Spec.CPU.Reserved)
	parseCPUSet(cpuSetIsolated, profile.Spec.CPU.Isolated)
	parseCPUSet(cpuSetShared, profile.Spec.CPU.Shared)
	parseCPUSet(cpuSetOfflined, profile.Spec.CPU.Offlined)

	return cpuSets
}

// createGuPod creates a 2-CPU Guaranteed QoS pod on the given node, waits for
// it to be ready, and returns it.
func createGuPod(ctx context.Context, node *corev1.Node) *corev1.Pod {
	GinkgoHelper()
	testpod := pods.GetTestPod()
	testpod.Namespace = testutils.NamespaceTesting
	testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("200Mi"),
		},
	}
	testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: node.Name}
	err := testclient.DataPlaneClient.Create(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())
	testpod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
	pods.DumpStateOnFailure(ctx, testclient.K8sClient, testpod, err)
	Expect(err).ToNot(HaveOccurred())
	Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
	testlog.Infof("GU pod %s pinned to cpus %s", testpod.Name, getGuPodCPUs(ctx, testpod))
	return testpod
}

// waitForDeploymentReady polls until the deployment has the expected number of
// ready replicas and returns the matching pod list.
func waitForDeploymentReady(ctx context.Context, dp *appsv1.Deployment, listOpts *client.ListOptions, replicas int32) *corev1.PodList {
	GinkgoHelper()
	podList := &corev1.PodList{}
	dpObj := client.ObjectKeyFromObject(dp)
	Eventually(func() bool {
		if err := testclient.DataPlaneClient.List(ctx, podList, listOpts); err != nil {
			return false
		}
		if err := testclient.DataPlaneClient.Get(ctx, dpObj, dp); err != nil {
			return false
		}
		if dp.Status.ReadyReplicas != replicas {
			testlog.Warningf("Deployment %q: %d/%d replicas ready", dpObj.String(), dp.Status.ReadyReplicas, replicas)
			return false
		}
		if len(podList.Items) == 0 {
			return false
		}
		for _, s := range podList.Items[0].Status.ContainerStatuses {
			if !s.Ready {
				return false
			}
		}
		return true
	}, 5*time.Minute, 10*time.Second).Should(BeTrue())
	return podList
}

// collectGuCPUsFromPodList lists pods matching the given options and returns
// the union of all their exclusively pinned CPUs.
func collectGuCPUsFromPodList(ctx context.Context, listOpts *client.ListOptions) cpuset.CPUSet {
	GinkgoHelper()
	podList := &corev1.PodList{}
	err := testclient.DataPlaneClient.List(ctx, podList, listOpts)
	Expect(err).ToNot(HaveOccurred())
	allCPUs := cpuset.New()
	for i := range podList.Items {
		allCPUs = allCPUs.Union(getGuPodCPUs(ctx, &podList.Items[i]))
	}
	return allCPUs
}

// getGuPodCPUs extracts the exclusively pinned CPUs from a Guaranteed QoS pod
// by running taskset inside the pod.
func getGuPodCPUs(ctx context.Context, pod *corev1.Pod) cpuset.CPUSet {
	GinkgoHelper()
	cmd := []string{"taskset", "-pc", "1"}
	outputb, err := pods.ExecCommandOnPod(testclient.K8sClient, pod, "", cmd)
	Expect(err).ToNot(HaveOccurred())
	parts := bytes.Split(outputb, []byte(":"))
	podcpus, err := cpuset.Parse(strings.TrimSpace(string(parts[1])))
	Expect(err).ToNot(HaveOccurred())
	return podcpus
}
