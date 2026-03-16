package __mixedcpus

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	kubeletconfig "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/schedstat"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/controller"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/runtime"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/deployments"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/resources"
)

const (
	kubeletMixedCPUsConfigFile = "/etc/kubernetes/openshift-workload-mixed-cpus"
	sharedCpusResource         = "workload.openshift.io/enable-shared-cpus"
	// the minimal number of cores for running the test is as follows:
	// reserved = one core, shared = one core, infra workload = one core, test pod = one core - 4 in total
	// smt alignment won't allow us to run the test pod with a single core, hence we should cancel it.
	numberOfCoresThatRequiredCancelingSMTAlignment = 4
	restartCooldownTime                            = 1 * time.Minute
	isolatedCpusEnv                                = "OPENSHIFT_ISOLATED_CPUS"
	sharedCpusEnv                                  = "OPENSHIFT_SHARED_CPUS"
	// DeploymentName contains the name of the deployment
	DeploymentName = "test-deployment"
)

var _ = Describe("Mixedcpus", Ordered, Label(string(label.MixedCPUs)), func() {
	ctx := context.Background()
	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("discovery mode enabled, performance profile not found")
		}
		teardown := setup(ctx)
		DeferCleanup(teardown, ctx)
	})

	Context("configuration files integrity", Label(string(label.Tier3)), func() {
		var profile *performancev2.PerformanceProfile
		BeforeEach(func() {
			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

		})

		It("should deploy kubelet configuration file", func() {
			workers, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			// test arbitrary one should be good enough
			worker := &workers[0]
			cmd := isFileExistCmd(kubeletMixedCPUsConfigFile)
			out, err := nodes.ExecCommand(ctx, worker, cmd)
			found := testutils.ToString(out)
			Expect(err).ToNot(HaveOccurred(), "failed to execute command on node; cmd=%q node=%q", cmd, worker)
			Expect(found).To(Equal("true"), "file not found; file=%q", kubeletMixedCPUsConfigFile)
		})

		It("should add Kubelet systemReservedCPUs the shared cpuset", func() {
			name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
			key := client.ObjectKey{Name: name}
			kc := &machineconfigv1.KubeletConfig{}
			Expect(testclient.ControlPlaneClient.Get(ctx, key, kc)).ToNot(HaveOccurred())
			k8sKc := &kubeletconfig.KubeletConfiguration{}
			Expect(json.Unmarshal(kc.Spec.KubeletConfig.Raw, k8sKc)).ToNot(HaveOccurred())
			reserved := mustParse(string(*profile.Spec.CPU.Reserved))
			shared := mustParse(string(*profile.Spec.CPU.Shared))
			reservedSystemCpus := mustParse(k8sKc.ReservedSystemCPUs)
			Expect(reservedSystemCpus.Equals(reserved.Union(*shared))).To(BeTrue(), "reservedSystemCPUs should contain the shared cpus; reservedSystemCPUs=%q reserved=%q shared=%q",
				reservedSystemCpus.String(), reserved.String(), shared.String())
		})

		It("should update runtime configuration with the given shared cpuset", func() {
			workers, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			// test arbitrary one should be good enough
			worker := &workers[0]
			cmd := []string{
				"chroot",
				"/rootfs",
				"/bin/bash",
				"-c",
				fmt.Sprintf("/bin/awk  -F '\"' '/shared_cpuset.*/ { print $2 }' %s", runtime.CRIORuntimeConfigFile),
			}
			out, err := nodes.ExecCommand(ctx, worker, cmd)
			cpus := testutils.ToString(out)
			Expect(err).ToNot(HaveOccurred(), "failed to execute command on node; cmd=%q node=%q", cmd, worker)
			cpus = strings.Trim(cpus, "\n")
			crioShared := mustParse(cpus)
			// don't need to check the error, the values were already validated.
			shared := mustParse(string(*profile.Spec.CPU.Shared))
			Expect(shared.Equals(*crioShared)).To(BeTrue(), "crio config file does not contain the expected shared cpuset; shared=%q crioShared=%q",
				shared.String(), crioShared.String())
		})
	})

	Context("single workload - request validation", Label(string(label.Tier3)), func() {
		var profile *performancev2.PerformanceProfile
		var getter cgroup.ControllersGetter
		BeforeEach(func() {
			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			// create test namespace
			ns := getTestingNamespace()
			Expect(testclient.DataPlaneClient.Create(ctx, &ns)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(testclient.DataPlaneClient.Delete(ctx, &ns)).ToNot(HaveOccurred())
				Expect(namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)).ToNot(HaveOccurred())
			})
			getter, err = cgroup.BuildGetter(ctx, testclient.DataPlaneClient, testclient.K8sClient)
			Expect(err).ToNot(HaveOccurred())
		})

		When("workloads requests access for shared cpus", func() {
			It("verify cpu load balancing still works with mixed cpus", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withAnnotations(map[string]string{"cpu-load-balancing.crio.io": "disable"}),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.CpuSet{}
				shared := mustParse(string(*profile.Spec.CPU.Shared))
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cpusIncludingShared, err := cpuset.Parse(cfg.Cpus)
				exclusivepodCpus := cpusIncludingShared.Difference(*shared)
				Expect(err).ToNot(HaveOccurred(), "unable to parse cpuset used by pod")
				// After the testpod is started get the schedstat and check for cpus
				// not participating in scheduling domains
				node := &corev1.Node{}
				err = testclient.DataPlaneClient.Get(ctx, client.ObjectKey{Name: p.Spec.NodeName}, node)
				Expect(err).ToNot(HaveOccurred())
				checkSchedulingDomains(node, exclusivepodCpus, func(cpuIDs cpuset.CPUSet) error {
					if !exclusivepodCpus.IsSubsetOf(cpuIDs) {
						return fmt.Errorf("pod CPUs NOT entirely part of cpus with load balance disabled: %v vs %v", exclusivepodCpus, cpuIDs)
					}
					return nil
				}, 2*time.Minute, 5*time.Second, "checking scheduling domains with pod running")

			})
			It("should have the shared cpus under its cgroups", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.CpuSet{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet := mustParse(cfg.Cpus)
				Expect(err).ToNot(HaveOccurred())
				shared := mustParse(string(*profile.Spec.CPU.Shared))
				Expect(cgroupCpuSet.Intersection(*shared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), shared.String())
			})
			It("should be able to disable cfs_quota", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withAnnotations(map[string]string{"cpu-quota.crio.io": "disable"}),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.Cpu{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				Expect(cfg.Quota).To(Or(Equal("max"), Equal("-1")))
			})
			It("should have OPENSHIFT_ISOLATED_CPUS and OPENSHIFT_SHARED_CPUS env variables under the container", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())

				By("checking environment variables are under the container's init process")
				shared := fetchSharedCPUsFromEnv(testclient.K8sClient, p, "")
				ppShared := mustParse(string(*profile.Spec.CPU.Shared))
				Expect(err).ToNot(HaveOccurred())
				Expect(shared.Equals(*ppShared)).To(BeTrue(), "OPENSHIFT_SHARED_CPUS value not equal to what configure in the performance profile."+
					"OPENSHIFT_SHARED_CPUS=%s spec.cpu.shared=%s", shared.String(), ppShared.String())

				By("checking environment variables are under the container's child process")
				cmd := printMixedCPUsEnvCmd()
				output, err := pods.ExecCommandOnPod(testclient.K8sClient, p, "", cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
				isolatedAndShared := strings.Split(string(output), "\n")
				isolated := mustParse(isolatedAndShared[0])
				Expect(isolated.IsEmpty()).ToNot(BeTrue(), "isolated cpuset is empty")
				shared = mustParse(isolatedAndShared[1])
				Expect(shared.IsEmpty()).ToNot(BeTrue(), "shared cpuset is empty")
				// we don't need to check the shared and isolated values again, we only want to make sure they appear
				// in the child processes
			})
			It("should contains the shared cpus after Kubelet restarts", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.CpuSet{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet, err := cpuset.Parse(cfg.Cpus)
				Expect(err).ToNot(HaveOccurred())
				shared, _ := cpuset.Parse(string(*profile.Spec.CPU.Shared))
				Expect(cgroupCpuSet.Intersection(shared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), shared.String())

				node := &corev1.Node{}
				err = testclient.DataPlaneClient.Get(ctx, client.ObjectKey{Name: p.Spec.NodeName}, node)
				Expect(err).ToNot(HaveOccurred())

				cmd := kubeletRestartCmd()
				// The command would fail since it aborts all the pods during restart
				_, _ = nodes.ExecCommand(ctx, node, cmd)
				// check that the node is ready after we restart Kubelet
				nodes.WaitForReadyOrFail("post restart", node.Name, 20*time.Minute, 3*time.Second)

				// giving kubelet more time to stabilize and initialize itself before
				// moving on with the testing.
				testlog.Infof("post restart: entering cooldown time: %v", restartCooldownTime)
				time.Sleep(restartCooldownTime)
				testlog.Infof("post restart: finished cooldown time: %v", restartCooldownTime)

				By("verifying that shared cpus are in the container's cgroup after kubelet restart")
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet, err = cpuset.Parse(cfg.Cpus)
				Expect(err).ToNot(HaveOccurred())
				Expect(cgroupCpuSet.Intersection(shared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), shared.String())
			})
		})

		When("Modifying the shared CPUs in the performance profile", func() {
			var newShared cpuset.CPUSet
			BeforeEach(func() {

				isolated := mustParse(string(*profile.Spec.CPU.Isolated))
				shared := mustParse(string(*profile.Spec.CPU.Shared))
				// select one arbitrary
				isolatedCore := mustParse(strconv.Itoa(isolated.List()[0]))
				sharedCore := mustParse(strconv.Itoa(shared.List()[0]))
				// swap one from the isolated with one from the shared
				//  to create new shared cpuset
				newIsolated := isolated.Difference(*isolatedCore).Union(*sharedCore)
				newShared = shared.Difference(*sharedCore).Union(*isolatedCore)
				profile.Spec.CPU.Isolated = cpuSetToPerformanceCPUSet(&newIsolated)
				profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&newShared)
				By("applying new performanceProfile")
				profiles.UpdateWithRetry(profile)
				poolName := poolname.GetByProfile(context.TODO(), profile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

				Expect(testclient.ControlPlaneClient.Get(ctx, client.ObjectKeyFromObject(profile), profile))
				testlog.Infof("new isolated CPU set=%q\nnew shared CPU set=%q", string(*profile.Spec.CPU.Isolated), string(*profile.Spec.CPU.Isolated))
				// we do not bother to revert the profile at the end of the test, since its irrelevant which of the cpus are shared
			})

			It("should contains the updated values under the container", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())

				By("checking cgroups under the container")
				cfg := &controller.CpuSet{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet := mustParse(cfg.Cpus)
				Expect(cgroupCpuSet.Intersection(newShared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), newShared.String())

				By("checking environment variables are under the container's init process")
				sharedFromEnv := fetchSharedCPUsFromEnv(testclient.K8sClient, p, "")
				Expect(sharedFromEnv.Equals(newShared)).To(BeTrue(), "OPENSHIFT_SHARED_CPUS value not equal to what configure in the performance profile."+
					"OPENSHIFT_SHARED_CPUS=%s spec.cpu.shared=%s", sharedFromEnv.String(), newShared.String())

				By("checking environment variables are under the container's child process")
				cmd := printMixedCPUsEnvCmd()
				output, err := pods.ExecCommandOnPod(testclient.K8sClient, p, "", cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
				isolatedAndShared := strings.Split(string(output), "\n")
				isolated := mustParse(isolatedAndShared[0])
				Expect(isolated.IsEmpty()).ToNot(BeTrue(), "isolated cpuset is empty")
				shared := mustParse(isolatedAndShared[1])
				Expect(shared.IsEmpty()).ToNot(BeTrue(), "shared cpuset is empty")
				// we don't need to check the shared and isolated values again, we only want to make sure they appear
				// in the child processes
			})
		})
		When("trying to deploy a burstable pod", func() {
			It("should cause the burstable pod to fail", func() {
				By("creating a burstable pod")
				rl := &corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1"),
					sharedCpusResource: resource.MustParse("1"),
				}
				_, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err.Error()).To(ContainSubstring("resource but pod is not Guaranteed QoS class"))
			})
		})
		When("trying to deploy a best effort pod", func() {
			It("should cause the best effort pod to fail", func() {
				By("creating a best effort pod")
				rl := &corev1.ResourceList{
					sharedCpusResource: resource.MustParse("1"),
				}
				_, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err.Error()).To(ContainSubstring("resource but pod is not Guaranteed QoS class"))
			})
		})
		When("creating a pod with resource openshift.io/enable-shared-cpus != 1", func() {
			It("should cause the pod to fail and not created", func() {
				By("creating a pod with an incompatible resource request")
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("2"),
				}
				_, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				// The full expectedError should be:
				// "more than a single \"workload.openshift.io/enable-shared-cpus\" resource is forbidden, please set the request to 1 or remove it"
				// however, the word forbidden is misspelt in openshift/kubernetes
				// https://github.com/openshift/kubernetes/blob/3c62f738ce74a624d46b4f73f25d6c15b3a80a2b/openshift-kube-apiserver/admission/autoscaling/mixedcpus/admission.go#L112
				expectedError := "more than a single \"workload.openshift.io/enable-shared-cpus\" resource is "
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedError))
			})
		})
		When("using namespace without mixedcpus allowed annotation", func() {
			It("should not be able to deploy a pod requesting shared cpus", func() {
				By("updating the namespace to disable mixedcpus")
				ns := getTestingNamespace()
				//Remove the annotations to disable mixedcpus
				ns.Annotations = nil
				Expect(testclient.DataPlaneClient.Update(ctx, &ns)).ToNot(HaveOccurred())
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}

				By("Creating a pod requesting shared cpus")
				_, err := createPod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl))
				Expect(err).To(HaveOccurred())
				expectedError := fmt.Sprintf("namespace %s is not allowed for workload.openshift.io/enable-shared-cpus resource request", ns.Name)
				Expect(err.Error()).To(ContainSubstring(expectedError))

			})
		})
		When("Disabling mixedCpus in the performance profile", func() {
			It("should causes all pods that uses shared CPUs to fail", func() {

				By("Creating a deployment with one pod asking for a shared cpu")
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p := makePod(ctx, testclient.DataPlaneClient, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				dp := deployments.Make(DeploymentName, testutils.NamespaceTesting,
					deployments.WithPodTemplate(p),
					deployments.WithNodeSelector(testutils.NodeSelectorLabels))

				Expect(testclient.DataPlaneClient.Create(ctx, dp)).ToNot(HaveOccurred())
				podList := &corev1.PodList{}
				listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
				Eventually(func() bool {
					isReady, err := deployments.IsReady(ctx, testclient.DataPlaneClient, listOptions, podList, dp)
					Expect(err).ToNot(HaveOccurred())
					return isReady
				}, time.Minute, time.Second).Should(BeTrue())
				Expect(testclient.DataPlaneClient.List(ctx, podList, listOptions)).To(Succeed())
				Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
				pod := podList.Items[0]

				By("Checking environment variable under the container")
				cmd := printMixedCPUsEnvCmd()
				output, err := pods.ExecCommandOnPod(testclient.K8sClient, &pod, "", cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
				isolatedAndShared := strings.Split(string(output), "\n")
				shared := mustParse(isolatedAndShared[1])
				ppShared := mustParse(string(*profile.Spec.CPU.Shared))
				Expect(err).ToNot(HaveOccurred())
				Expect(shared.Equals(*ppShared)).To(BeTrue(), "OPENSHIFT_SHARED_CPUS value not equal to what configure in the performance profile."+
					"OPENSHIFT_SHARED_CPUS=%s spec.cpu.shared=%s", shared.String(), ppShared.String())
				testlog.Infof("shared CPU set=%q", shared.String())

				By("Editing performanceProfile to have empty shared cpus and setting mixedCpus to false")
				profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&cpuset.CPUSet{})
				profile.Spec.WorkloadHints.MixedCpus = ptr.To(false)

				By("Applying new performanceProfile")
				profiles.UpdateWithRetry(profile)
				poolName := poolname.GetByProfile(context.TODO(), profile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

				By("Verifying that the pod is failed to be scheduled")
				podList = &corev1.PodList{}
				Expect(testclient.DataPlaneClient.List(ctx, podList, listOptions)).To(Succeed())
				Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
				pod = podList.Items[0]
				Eventually(func() bool {
					isFailed, err := pods.CheckPODSchedulingFailed(testclient.DataPlaneClient, &pod)
					Expect(err).ToNot(HaveOccurred())
					return isFailed
				}, time.Minute, time.Second).Should(BeTrue())
				Expect(pod.Status.Phase).To(Equal(corev1.PodPending), "Pod %s is not in the pending state", pod.Name)

				By("Reverting the cluster to previous state")
				Expect(testclient.ControlPlaneClient.Get(ctx, client.ObjectKeyFromObject(profile), profile))
				profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(ppShared)
				profile.Spec.WorkloadHints.MixedCpus = ptr.To(true)
				profiles.UpdateWithRetry(profile)
				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
			})
		})
	})

	Context("Check exec-cpu-affinity feature", func() {
		BeforeEach(func() {
			ns := getTestingNamespace()
			Expect(testclient.DataPlaneClient.Create(ctx, &ns)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(testclient.DataPlaneClient.Delete(ctx, &ns)).ToNot(HaveOccurred())
				Expect(namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)).ToNot(HaveOccurred())
			})
		})

		When("exec-cpu-affinity is enabled (default in PP)", func() {
			var workerRTNode *corev1.Node
			var profile, initialProfile *performancev2.PerformanceProfile
			var getter cgroup.ControllersGetter
			var updatedShared, updatedIsolated cpuset.CPUSet
			var needsUpdate bool

			BeforeEach(func() {
				needsUpdate = false

				By("Checking if exec-cpu-affinity is enabled by default in the profile")
				var err error
				profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				Expect(profile).ToNot(BeNil(), "Failed to get performance profile")
				initialProfile = profile.DeepCopy()
				val, ok := profile.Annotations[performancev2.PerformanceProfileExecCPUAffinityAnnotation]
				if ok {
					Expect(val).NotTo(Equal(performancev2.PerformanceProfileExecCPUAffinityDisable), "exec-cpu-affinity is disabled in the profile")
				}

				workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
				Expect(workerRTNodes).ToNot(BeEmpty())
				workerRTNode = &workerRTNodes[0]

				getter, err = cgroup.BuildGetter(ctx, testclient.DataPlaneClient, testclient.K8sClient)
				Expect(err).ToNot(HaveOccurred())

				By("Updating performance profile to have enough shared cpus if needed")
				updatedIsolated = *mustParse(string(*profile.Spec.CPU.Isolated))
				updatedShared = *mustParse(string(*profile.Spec.CPU.Shared))

				if updatedShared.Size() < 2 {
					testlog.Info("shared cpuset has less than 2 cpus; this test requires at least 2 shared cpus; update the profile")

					// we need at least 4 total isolated and shared CPUs:
					// 1 as a buffer for node's base load
					// 1 as the test gu pod requests
					// 2 as shared cpus
					minIsolatedCpus := 3
					if updatedShared.Size() == 0 {
						minIsolatedCpus = 4
					}
					if updatedIsolated.Size() < minIsolatedCpus {
						Skip(fmt.Sprintf("isolated cpuset has less than %d cpus; this test requires at least another %d isolated cpus", minIsolatedCpus, minIsolatedCpus))
					}

					coreSiblings, err := nodes.GetCoreSiblings(ctx, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					updatedShared, err = nodes.GetTwoSiblingsFromCPUSet(coreSiblings, updatedIsolated)
					if err != nil {
						testlog.Info("no two siblings found in the given CPU set, looks like the initial profile does not respect hyperthreading; proceed then with this state and pick first two isolated CPUs as the shared CPUs")
						updatedShared = cpuset.New(updatedIsolated.List()[0], updatedIsolated.List()[1])
					}

					updatedIsolated = updatedIsolated.Difference(updatedShared)

					testlog.Infof("CPU update:shared cpu %q isolated cpus %q", updatedShared.String(), updatedIsolated.String())
					profile.Spec.CPU.Isolated = cpuSetToPerformanceCPUSet(&updatedIsolated)
					profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&updatedShared)
					profile.Spec.WorkloadHints.MixedCpus = ptr.To(true) // if not already

					profiles.UpdateWithRetry(profile)

					poolName := poolname.GetByProfile(context.TODO(), profile)
					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(context.TODO(), profile)
					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
					needsUpdate = true
				}
			})

			AfterEach(func() {
				if !needsUpdate {
					By("no need for a revert")
					return
				}

				By("Reverting the cluster to previous state")
				profiles.UpdateWithRetry(initialProfile)
				poolName := poolname.GetByProfile(context.TODO(), initialProfile)
				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(context.TODO(), initialProfile)
				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(context.TODO(), initialProfile)
			})

			DescribeTable("should pin exec process to the first CPU that belongs to the right CPU set in the container, that is expected to take charge of exec process (isolated or shared)", func(qos corev1.PodQOSClass, containersResources []corev1.ResourceList) {
				By("Creating the test pod")
				isolatedCpus, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
				Expect(err).ToNot(HaveOccurred(), "Failed to parse isolated CPUs")
				switch qos {
				case corev1.PodQOSGuaranteed:
					totalPodCpus := resources.TotalCPUsRoundedUp(containersResources)
					if isolatedCpus.Size() < totalPodCpus {
						Skip("Skipping test: Insufficient isolated CPUs")
					}
				case corev1.PodQOSBurstable:
					maxPodCpus := resources.MaxCPURequestsRoundedUp(containersResources)
					if isolatedCpus.Size() < maxPodCpus {
						Skip("Skipping test: Insufficient isolated CPUs")
					}
				case corev1.PodQOSBestEffort:
					testlog.Info("test best-effort pod")
				default:
					Fail("Invalid QoS class")
				}

				testPod, err := pods.MakePodWithResources(workerRTNode.Name, qos, containersResources)
				Expect(err).ToNot(HaveOccurred(), "Failed to make test pod")
				Expect(testclient.DataPlaneClient.Create(ctx, testPod)).To(Succeed(), "Failed to create test pod")
				DeferCleanup(func() {
					Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, testPod)).To(Succeed())
				})
				testPod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testPod), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())

				cpusetCfg := &controller.CpuSet{}
				for _, container := range testPod.Spec.Containers {
					/* we need to verify the below scenarios:
					 1. non gu pod -> exec process will be pinned to any CPU
					 2. gu pods:
					    2.1 containers requesting whole CPUs:
							a. with Shared CPU -> exec process is pinned to the FIRST SHARED CPU
							b. without Shared CPU -> exec process is pinned to the FIRST ISOLATED CPU of the container
						2.2 containers requesting fractional CPUs -> exec process is pinned to ANY CPU
					*/

					isExclusiveCPURequest := false
					retries := 1

					if qos == corev1.PodQOSGuaranteed {
						milliCPU := container.Resources.Requests.Cpu().MilliValue()
						isExclusiveCPURequest = (milliCPU % 1000) == 0
					}

					if !isExclusiveCPURequest {
						testlog.Infof("exec process should be pinned to any CPU of the isolated set")
					}

					By(fmt.Sprintf("Prepare comparable data for container %s, resource list: %v", container.Name, container.Resources.String()))
					Expect(getter.Container(ctx, testPod, container.Name, cpusetCfg)).To(Succeed(), "Failed to get cpuset config for the container")

					cpusIncludingShared, err := cpuset.Parse(cpusetCfg.Cpus)
					Expect(err).ToNot(HaveOccurred(), "Failed to parse cpuset config for test pod cpus=%q", cpusetCfg.Cpus)
					Expect(cpusIncludingShared.Size()).ToNot(BeZero(), "Failed to get cpuset config for test pod cpus=%q", cpusetCfg.Cpus)
					testlog.Infof("all CPUs dedicated for the container (including shared if requested): %s", cpusIncludingShared.String())
					firstCPU := cpusIncludingShared.List()[0]

					isSharedCPUsRequested := container.Resources.Limits.Name(sharedCpusResource, resource.DecimalSI).Value() > 0
					if isSharedCPUsRequested {
						cntShared := cpusIncludingShared.Difference(updatedIsolated)
						reserved := mustParse(string(*profile.Spec.CPU.Reserved))
						cntShared = cntShared.Difference(*reserved)
						Expect(cntShared.Size()).ToNot(BeZero(), "no shared CPUs found for container %s", container.Name)
						Expect(cntShared.Equals(updatedShared)).To(BeTrue(), "shared CPUs are not as configured in the profile; found=%s, expected=%s", cntShared.String(), updatedShared.String())
						firstCPU = cntShared.List()[0]
						testlog.Infof("container %s: first shared CPU: %d; all shared CPUs: %s", container.Name, firstCPU, cntShared.String())
					}

					// try high enough times to ensure that even with low cpus count, the functionality is preserved
					if isExclusiveCPURequest {
						cpuRequest := container.Resources.Requests.Name(corev1.ResourceCPU, resource.DecimalSI).Value()
						if isSharedCPUsRequested {
							cpuRequest = int64(updatedShared.Size())
						}

						// The concept of retries is to avoid false positives on first check.
						// especially when the number of allowed cpus is low, the possibility to
						// choose the correct (first) CPU is anyway high; with retries we ensure
						// that even with this high possibility, retrying many times still gives
						// us the correct result.
						// the formula is to start with 5 retries (`oc exec` entries to the container)
						// for CPUset of size 2, and reduce that when the number is higher, down
						// to 1 retry minimum.
						retries = int(math.Ceil(float64(10) / float64(cpuRequest)))
					}

					testlog.Infof("expected CPU affinity: pinToFirstCPU: %t, retries: %d", isExclusiveCPURequest, retries)
					if isExclusiveCPURequest {
						testlog.Infof("expected CPU affinity: affinityToShared: %t, affinityToIsolated: %t", isSharedCPUsRequested, !isSharedCPUsRequested)
					} else {
						testlog.Infof("expected CPU affinity: Any CPU from the allowed list")
					}

					// Sample the pinned CPU multiple times per run to ensure the process stays pinned (not a single racy snapshot).
					// Field 39 in /proc/<pid>/stat is the processor number.
					// See proc_pid_stat(5): https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html
					By(fmt.Sprintf("Run exec commands on the pod in parallel and verify the process is pinned to the right CPU for container %s", container.Name))
					const samplesPerRun = 5
					const sampleIntervalSec = 1
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf(
						"sleep 10 & SLPID=$!; sleep 0.5; for ((i=1;i<=%d;i++)); do awk '{print $39}' /proc/$SLPID/stat; sleep %d; done",
						samplesPerRun, sampleIntervalSec)}
					execProcessCPUSamples := make([][]int, retries)
					g, _ := errgroup.WithContext(ctx)
					for i := 0; i < retries; i++ {
						idx := i
						g.Go(func() error {
							output, err := pods.ExecCommandOnPod(testclient.K8sClient, testPod, container.Name, cmd)
							if err != nil {
								return fmt.Errorf("exec command failed for run %d: %w", idx, err)
							}
							strout := strings.TrimSpace(string(output))
							testlog.Infof("entry %d: exec command output (samples): \n%s", idx, strout)
							var samples []int
							for _, line := range strings.Split(strout, "\n") {
								line = strings.TrimSpace(line)
								if line == "" {
									continue
								}
								cpu, err := strconv.Atoi(line)
								if err != nil {
									return fmt.Errorf("entry %d: failed to parse processor number from %q: %w", idx, line, err)
								}
								samples = append(samples, cpu)
							}
							if len(samples) == 0 {
								return fmt.Errorf("entry %d: no valid samples", idx)
							}
							if len(samples) < samplesPerRun {
								testlog.Warningf("entry %d: got %d/%d samples (process may have exited early)", idx, len(samples), samplesPerRun)
							}
							execProcessCPUSamples[idx] = samples
							return nil
						})
					}
					Expect(g.Wait()).ToNot(HaveOccurred(), "One or more exec commands failed")
					for i := 0; i < retries; i++ {
						testlog.Infof("process samples of entry %d: %v", i, execProcessCPUSamples[i])
						for _, cpu := range execProcessCPUSamples[i] {
							if isExclusiveCPURequest {
								Expect(cpu).To(Equal(firstCPU), "Exec process CPU is not the correct first CPU")
							} else {
								Expect(cpusIncludingShared.Contains(cpu)).To(BeTrue(), "Exec process CPU %d is not part of the cpuset: %s", cpu, cpusIncludingShared.String())
							}
						}
					}
				}
			},
				Entry("guaranteed pod with mixed containers: exec process on container with whole CPUs should be pinned to the first shared CPU; and on container with fractional CPUs should be pinned to any CPU even if shared is specified",
					corev1.PodQOSGuaranteed,
					[]corev1.ResourceList{
						{ // cnt1 resources
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
							sharedCpusResource:    resource.MustParse("1"),
						},
						{ // cnt2 resources
							corev1.ResourceCPU:    resource.MustParse("2300m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
							sharedCpusResource:    resource.MustParse("1"),
						},
					}),
				Entry("guaranteed pod with mixed containers: with shared CPU and without shared CPU",
					corev1.PodQOSGuaranteed,
					[]corev1.ResourceList{
						{ // cnt1 resources
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
							sharedCpusResource:    resource.MustParse("1"),
						},
						{ // cnt2 resources
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
						{ // cnt3 resources
							corev1.ResourceCPU:    resource.MustParse("2300m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					}),
				Entry("best-effort pod with shared CPU request",
					corev1.PodQOSBestEffort,
					// shared CPUs are only allowed for guaranteed pods
					[]corev1.ResourceList{
						//cnt1 resources
						{},
					}),
				Entry("burstable pod with shared CPU request",
					corev1.PodQOSBurstable,
					// shared CPUs are only allowed for guaranteed pods
					[]corev1.ResourceList{
						{ // cnt1 resources
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					}),
			)
		})
	})
})

func setup(ctx context.Context) func(ctx2 context.Context) {
	var updateNeeded bool
	profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	initialProfile := profile.DeepCopy()

	if !profileutil.IsMixedCPUsEnabled(profile) {
		isolated := mustParse(string(*profile.Spec.CPU.Isolated))

		// arbitrary take the first one
		sharedcpu := cpuset.New(isolated.List()[0])
		testlog.Infof("shared cpu ids are: %q", sharedcpu.String())
		updatedIsolated := isolated.Difference(sharedcpu)
		profile.Spec.CPU.Isolated = cpuSetToPerformanceCPUSet(&updatedIsolated)
		profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&sharedcpu)
		profile.Spec.WorkloadHints.MixedCpus = ptr.To(true)
		testlog.Infof("enable mixed cpus for profile %q", profile.Name)
		updateNeeded = true
	} else {
		testlog.Infof("mixed cpus already enabled for profile %q", profile.Name)
	}

	workers, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	for _, worker := range workers {
		//node cpu numbers are integral
		numOfCores, _ := worker.Status.Capacity.Cpu().AsInt64()
		if numOfCores <= numberOfCoresThatRequiredCancelingSMTAlignment {
			profile.Annotations = map[string]string{
				"kubeletconfig.experimental": "{\"cpuManagerPolicyOptions\": {\"full-pcpus-only\": \"false\"}}",
			}
			testlog.Infof("canceling SMT alignment for nodes under profile %q", profile.Name)
			updateNeeded = true
		}
	}

	if !updateNeeded {
		return func(ctx context.Context) {
			By(fmt.Sprintf("skipping teardown - no changes to profile %q were applied", profile.Name))
		}
	}
	profiles.UpdateWithRetry(profile)
	poolName := poolname.GetByProfile(context.TODO(), profile)
	By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
	profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

	By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
	profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

	teardown := func(ctx2 context.Context) {
		By(fmt.Sprintf("executing teardown - revert profile %q back to its initial state", profile.Name))
		Expect(testclient.ControlPlaneClient.Get(ctx2, client.ObjectKeyFromObject(initialProfile), profile))
		profiles.UpdateWithRetry(initialProfile)

		// do not wait if nothing has changed
		if initialProfile.ResourceVersion != profile.ResourceVersion {
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		}
	}
	return teardown
}

func cpuSetToPerformanceCPUSet(set *cpuset.CPUSet) *performancev2.CPUSet {
	c := performancev2.CPUSet(set.String())
	return &c
}

func printMixedCPUsEnvCmd() []string {
	return []string{
		"/bin/printenv",
		"OPENSHIFT_ISOLATED_CPUS",
		"OPENSHIFT_SHARED_CPUS",
	}
}

// checks whether file exists and not empty
func isFileExistCmd(absoluteFileName string) []string {
	return []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		fmt.Sprintf("if [[ -s %s ]]; then echo true; else echo false; fi", absoluteFileName),
	}
}

func kubeletRestartCmd() []string {
	return []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		"systemctl restart kubelet",
	}
}

func createPod(ctx context.Context, c client.Client, ns string, opts ...func(pod *corev1.Pod)) (*corev1.Pod, error) {
	p := makePod(ctx, c, ns, opts...)
	if err := c.Create(ctx, p); err != nil {
		return nil, err
	}
	p, err := pods.WaitForCondition(ctx, client.ObjectKeyFromObject(p), corev1.PodReady, corev1.ConditionTrue, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to create pod. pod=%s, podStatus=%v err=%v", client.ObjectKeyFromObject(p).String(), p.Status, err)
	}
	return p, nil
}

func makePod(ctx context.Context, c client.Client, ns string, opts ...func(pod *corev1.Pod)) *corev1.Pod {
	p := pods.GetTestPod()
	p.Namespace = ns
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func withRequests(rl *corev1.ResourceList) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Requests = *rl
	}
}

func withLimits(rl *corev1.ResourceList) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Limits = *rl
	}
}

func withAnnotations(annot map[string]string) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		if p.Annotations == nil {
			p.Annotations = map[string]string{}
		}
		for k, v := range annot {
			p.Annotations[k] = v
		}
	}
}

func withRuntime(name string) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.RuntimeClassName = &name
	}
}

func getTestingNamespace() corev1.Namespace {
	return *namespaces.TestingNamespace
}

func mustParse(cpus string) *cpuset.CPUSet {
	GinkgoHelper()
	set, err := cpuset.Parse(cpus)
	Expect(err).ToNot(HaveOccurred(), "failed to parse cpuset; cpus=%q", cpus)
	return &set
}

func fetchSharedCPUsFromEnv(c *kubernetes.Clientset, p *corev1.Pod, containerName string) *cpuset.CPUSet {
	GinkgoHelper()
	// we should check the environment variable under the init process (pid 1)
	cmd := []string{
		"/bin/cat",
		"/proc/1/environ",
	}
	output, err := pods.ExecCommandOnPod(c, p, containerName, cmd)
	Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
	Expect(string(output)).To(And(ContainSubstring(isolatedCpusEnv), ContainSubstring(sharedCpusEnv)))
	// \x00 is the NULL (C programming) of UTF-8
	envs := strings.Split(string(output), "\x00")
	var shared *cpuset.CPUSet
	for _, env := range envs {
		if strings.HasPrefix(env, sharedCpusEnv) {
			shared = mustParse(strings.Split(env, "=")[1])
		}
	}
	return shared
}

// getCPUswithLoadBalanceDisabled Return cpus which are not in any scheduling domain
func getCPUswithLoadBalanceDisabled(ctx context.Context, targetNode *corev1.Node) ([]string, error) {
	cmd := []string{"/bin/bash", "-c", "cat /proc/schedstat"}
	out, err := nodes.ExecCommand(ctx, targetNode, cmd)
	schedstatData := testutils.ToString(out)
	if err != nil {
		return nil, err
	}

	info, err := schedstat.ParseData(strings.NewReader(schedstatData))
	if err != nil {
		return nil, err
	}

	cpusWithoutDomain := []string{}
	for _, cpu := range info.GetCPUs() {
		doms, ok := info.GetDomains(cpu)
		if !ok {
			return nil, fmt.Errorf("unknown cpu: %v", cpu)
		}
		if len(doms) > 0 {
			continue
		}
		cpusWithoutDomain = append(cpusWithoutDomain, cpu)
	}

	return cpusWithoutDomain, nil
}

// checkSchedulingDomains Check cpus are part of any scheduling domain
func checkSchedulingDomains(workerRTNode *corev1.Node, podCpus cpuset.CPUSet, testFunc func(cpuset.CPUSet) error, timeout, polling time.Duration, errMsg string) {
	Eventually(func() error {
		cpusNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(context.TODO(), workerRTNode)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("cpus with load balancing disabled are: %v", cpusNotInSchedulingDomains)
		Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus with load balancing disabled from /proc/schedstat")
		cpuIDList, err := schedstat.MakeCPUIDListFromCPUList(cpusNotInSchedulingDomains)
		if err != nil {
			return err
		}
		cpuIDs := cpuset.New(cpuIDList...)
		return testFunc(cpuIDs)
	}).WithTimeout(timeout).WithPolling(polling).ShouldNot(HaveOccurred(), errMsg)
}
