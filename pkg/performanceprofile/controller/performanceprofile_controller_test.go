package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	igntypes "github.com/coreos/ignition/config/v2_2/types"
	apiconfigv1 "github.com/openshift/api/config/v1"
	configv1 "github.com/openshift/api/config/v1"
	apifeatures "github.com/openshift/api/features"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/handler"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/kubeletconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/runtimeclass"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/tuned"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	hcpcomponents "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/components"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
	hcpstatus "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/status"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"
	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/testing"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type reconcilerBuilderFunc func(client.Object, ...runtime.Object) *PerformanceProfileReconciler

const (
	performanceProfileName      = "pp-test"
	hostedControlPlaneNamespace = "ns-test"
)

var _ = Describe("Controller", func() {
	DescribeTableSubtree("Running on different platforms", func(newFakeReconciler reconcilerBuilderFunc, finalizer string, isHypershift bool) {
		var instance client.Object
		var request reconcile.Request
		var profile *performancev2.PerformanceProfile
		var profileMCP *mcov1.MachineConfigPool
		var infra *apiconfigv1.Infrastructure
		var clusterOperator *apiconfigv1.ClusterOperator
		BeforeEach(func() {
			profileMCP = testutils.NewProfileMCP()
			profile = testutils.NewPerformanceProfile(performanceProfileName)
			instance = profile
			infra = testutils.NewInfraResource(false)
			clusterOperator = testutils.NewClusterOperator()
			if isHypershift {
				Expect(os.Setenv("HYPERSHIFT", "true")).To(Succeed())
				Expect(os.Setenv("MY_NAMESPACE", hostedControlPlaneNamespace)).To(Succeed())
				instance = encapsulateProfileInConfigMap(profile)
			}
			request = reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: instance.GetNamespace(),
					Name:      instance.GetName(),
				},
			}
		})

		It("should add finalizer to the performance profile", func() {
			r := newFakeReconciler(instance, profileMCP, infra, clusterOperator)
			Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))
			Expect(r.ManagementClient.Get(context.TODO(), client.ObjectKeyFromObject(instance), instance)).To(Succeed())
			Expect(hasFinalizer(instance, finalizer)).To(Equal(true))
		})

		Context("with profile with finalizer", func() {
			BeforeEach(func() {
				instance.SetFinalizers([]string{finalizer})
			})

			It("should create all resources on first reconcile loop", func() {
				r := newFakeReconciler(instance, profileMCP, infra, clusterOperator)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				key := types.NamespacedName{
					Name:      machineconfig.GetMachineConfigName(profile.Name),
					Namespace: metav1.NamespaceNone,
				}

				// verify MachineConfig creation
				mc := &mcov1.MachineConfig{}
				err := r.ManagementClient.Get(context.TODO(), key, mc)
				Expect(err).ToNot(HaveOccurred())

				key = types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
					Namespace: metav1.NamespaceNone,
				}

				// verify KubeletConfig creation
				kc := &mcov1.KubeletConfig{}
				err = r.ManagementClient.Get(context.TODO(), key, kc)
				Expect(err).ToNot(HaveOccurred())

				// verify RuntimeClass creation
				runtimeClass := &nodev1.RuntimeClass{}
				err = r.Get(context.TODO(), key, runtimeClass)
				Expect(err).ToNot(HaveOccurred())

				// verify tuned performance creation
				tunedPerformance := &tunedv1.Tuned{}
				key.Name = components.GetComponentName(profile.Name, components.ProfileNamePerformance)
				key.Namespace = components.NamespaceNodeTuningOperator
				err = r.ManagementClient.Get(context.TODO(), key, tunedPerformance)
				Expect(err).ToNot(HaveOccurred())
			})

			It("should create event on the second reconcile loop", func() {
				r := newFakeReconciler(instance, profileMCP, infra, clusterOperator)

				Expect(reconcileTimes(r, request, 2)).To(Equal(reconcile.Result{}))

				// verify creation event
				fakeRecorder, ok := r.Recorder.(*record.FakeRecorder)
				Expect(ok).To(BeTrue())
				event := <-fakeRecorder.Events
				Expect(event).To(ContainSubstring("Creation succeeded"))
			})

			It("should update the profile status", func() {
				r := newFakeReconciler(instance, profileMCP, infra, clusterOperator)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				updatedProfile := &performancev2.PerformanceProfile{}
				key := types.NamespacedName{
					Name:      profile.Name,
					Namespace: metav1.NamespaceNone,
				}
				Expect(r.ManagementClient.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())

				// verify performance profile status
				Expect(len(updatedProfile.Status.Conditions)).To(Equal(4))

				// verify profile conditions
				progressingCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionProgressing)
				Expect(progressingCondition).ToNot(BeNil())
				Expect(progressingCondition.Status).To(Equal(corev1.ConditionFalse))
				availableCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionAvailable)
				Expect(availableCondition).ToNot(BeNil())
				Expect(availableCondition.Status).To(Equal(corev1.ConditionTrue))
			})

			It("should promote kubelet config failure condition", func() {
				skipForHypershift()
				r := newFakeReconciler(profile, profileMCP, infra, clusterOperator)
				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				key := types.NamespacedName{
					Name:      name,
					Namespace: metav1.NamespaceNone,
				}

				kc := &mcov1.KubeletConfig{}
				err := r.Get(context.TODO(), key, kc)
				Expect(err).ToNot(HaveOccurred())

				now := time.Now()
				kc.Status.Conditions = []mcov1.KubeletConfigCondition{
					{
						Type:               mcov1.KubeletConfigFailure,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: now.Add(time.Minute)},
						Reason:             "Test failure condition",
						Message:            "Test failure condition",
					},
					{
						Type:               mcov1.KubeletConfigSuccess,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: now},
						Reason:             "Test succeed condition",
						Message:            "Test succeed condition",
					},
				}
				Expect(r.Update(context.TODO(), kc)).ToNot(HaveOccurred())

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				updatedProfile := &performancev2.PerformanceProfile{}
				key = types.NamespacedName{
					Name:      profile.Name,
					Namespace: metav1.NamespaceNone,
				}
				Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())

				degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
				Expect(degradedCondition.Status).To(Equal(corev1.ConditionTrue))
				Expect(degradedCondition.Message).To(Equal("Test failure condition"))
				Expect(degradedCondition.Reason).To(Equal(status.ConditionKubeletFailed))
			})

			It("should not promote old failure condition", func() {
				skipForHypershift()
				r := newFakeReconciler(profile, profileMCP, infra, clusterOperator)
				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				key := types.NamespacedName{
					Name:      name,
					Namespace: metav1.NamespaceNone,
				}

				kc := &mcov1.KubeletConfig{}
				err := r.Get(context.TODO(), key, kc)
				Expect(err).ToNot(HaveOccurred())

				now := time.Now()
				kc.Status.Conditions = []mcov1.KubeletConfigCondition{
					{
						Type:               mcov1.KubeletConfigFailure,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: now},
						Reason:             "Test failure condition",
						Message:            "Test failure condition",
					},
					{
						Type:               mcov1.KubeletConfigSuccess,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Time{Time: now.Add(time.Minute)},
						Reason:             "Test succeed condition",
						Message:            "Test succeed condition",
					},
				}
				Expect(r.Update(context.TODO(), kc)).ToNot(HaveOccurred())

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				updatedProfile := &performancev2.PerformanceProfile{}
				key = types.NamespacedName{
					Name:      profile.Name,
					Namespace: metav1.NamespaceNone,
				}
				Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())

				degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
				Expect(degradedCondition.Status).To(Equal(corev1.ConditionFalse))
			})

			It("should remove outdated tuned objects", func() {
				skipForHypershift()
				tunedOutdatedA, err := tuned.NewNodePerformance(profile)
				Expect(err).ToNot(HaveOccurred())
				tunedOutdatedA.Name = "outdated-a"
				tunedOutdatedA.OwnerReferences = []metav1.OwnerReference{
					{Name: profile.Name},
				}
				tunedOutdatedB, err := tuned.NewNodePerformance(profile)
				Expect(err).ToNot(HaveOccurred())
				tunedOutdatedB.Name = "outdated-b"
				tunedOutdatedB.OwnerReferences = []metav1.OwnerReference{
					{Name: profile.Name},
				}
				r := newFakeReconciler(profile, tunedOutdatedA, tunedOutdatedB, profileMCP, infra, clusterOperator)

				keyA := types.NamespacedName{
					Name:      tunedOutdatedA.Name,
					Namespace: tunedOutdatedA.Namespace,
				}
				ta := &tunedv1.Tuned{}
				err = r.Get(context.TODO(), keyA, ta)
				Expect(err).ToNot(HaveOccurred())

				keyB := types.NamespacedName{
					Name:      tunedOutdatedA.Name,
					Namespace: tunedOutdatedA.Namespace,
				}
				tb := &tunedv1.Tuned{}
				err = r.Get(context.TODO(), keyB, tb)
				Expect(err).ToNot(HaveOccurred())

				result, err := r.Reconcile(context.TODO(), request)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				tunedList := &tunedv1.TunedList{}
				err = r.List(context.TODO(), tunedList)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(tunedList.Items)).To(Equal(1))
				tunedName := components.GetComponentName(profile.Name, components.ProfileNamePerformance)
				Expect(tunedList.Items[0].Name).To(Equal(tunedName))
			})

			It("should create nothing when pause annotation is set", func() {
				profile.Annotations = map[string]string{performancev2.PerformanceProfilePauseAnnotation: "true"}
				if isHypershift {
					instance = encapsulateProfileInConfigMap(profile)
				}
				r := newFakeReconciler(instance, profileMCP, infra, clusterOperator)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				key := types.NamespacedName{
					Name:      name,
					Namespace: metav1.NamespaceNone,
				}

				// verify MachineConfig wasn't created
				mc := &mcov1.MachineConfig{}
				err := r.ManagementClient.Get(context.TODO(), key, mc)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				// verify that KubeletConfig wasn't created
				kc := &mcov1.KubeletConfig{}
				err = r.ManagementClient.Get(context.TODO(), key, kc)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				// verify no machine config pool was created
				mcp := &mcov1.MachineConfigPool{}
				err = r.ManagementClient.Get(context.TODO(), key, mcp)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				// verify tuned Performance wasn't created
				tunedPerformance := &tunedv1.Tuned{}
				key.Name = components.ProfileNamePerformance
				key.Namespace = components.NamespaceNodeTuningOperator
				err = r.ManagementClient.Get(context.TODO(), key, tunedPerformance)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				// verify that no RuntimeClass was created
				runtimeClass := &nodev1.RuntimeClass{}
				err = r.Get(context.TODO(), key, runtimeClass)
				Expect(errors.IsNotFound(err)).To(BeTrue())
			})

			Context("when all components exist", func() {
				var mc *mcov1.MachineConfig
				var kc *mcov1.KubeletConfig
				var tunedPerformance *tunedv1.Tuned
				var runtimeClass *nodev1.RuntimeClass

				BeforeEach(func() {
					skipForHypershift()
					var err error

					mc, err = machineconfig.New(profile, &components.MachineConfigOptions{PinningMode: &infra.Status.CPUPartitioning})
					Expect(err).ToNot(HaveOccurred())

					mcpSelectorKey, mcpSelectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
					kc, err = kubeletconfig.New(profile, &components.KubeletConfigOptions{MachineConfigPoolSelector: map[string]string{mcpSelectorKey: mcpSelectorValue}})
					Expect(err).ToNot(HaveOccurred())

					tunedPerformance, err = tuned.NewNodePerformance(profile)
					Expect(err).ToNot(HaveOccurred())

					runtimeClass = runtimeclass.New(profile, machineconfig.HighPerformanceRuntime)
				})

				It("should not record new create event", func() {
					r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP, infra, clusterOperator)

					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					// verify that no creation event created
					fakeRecorder, ok := r.Recorder.(*record.FakeRecorder)
					Expect(ok).To(BeTrue())

					select {
					case <-fakeRecorder.Events:
						Fail("the recorder should not have new events")
					default:
					}
				})

				DescribeTable("MachineConfig kernelType updates",
					func(rtKernelEnabled bool, kernelPageSize *performancev2.KernelPageSize, expectedKernelType string) {
						// Set up profile
						profile.Spec.RealTimeKernel.Enabled = ptr.To(rtKernelEnabled)
						profile.Spec.KernelPageSize = kernelPageSize

						r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator)

						Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

						// Verify MachineConfig update
						key := types.NamespacedName{
							Name:      machineconfig.GetMachineConfigName(profile.Name),
							Namespace: metav1.NamespaceNone,
						}

						mc := &mcov1.MachineConfig{}
						err := r.Get(context.TODO(), key, mc)
						Expect(err).ToNot(HaveOccurred())
						Expect(mc.Spec.KernelType).To(Equal(expectedKernelType))
					},
					Entry("should set kernelType to default when RealTimeKernel is disabled", false, nil, machineconfig.MCKernelDefault),
					Entry("should set kernelType to 64k-pages when RT kernel disabled and 64k kernel page size selected", false, ptr.To(performancev2.KernelPageSize("64k")), machineconfig.MCKernel64kPages),
					Entry("should set kernelType to realtime when RT kernel is enabled", true, nil, machineconfig.MCKernelRT),
				)

				It("should update MC, KC and Tuned when CPU params change", func() {
					reserved := performancev2.CPUSet("0-1")
					isolated := performancev2.CPUSet("2-3")
					profile.Spec.CPU = &performancev2.CPU{
						Reserved: &reserved,
						Isolated: &isolated,
					}

					r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator)

					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					key := types.NamespacedName{
						Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
						Namespace: metav1.NamespaceNone,
					}

					By("Verifying KC update for reserved")
					kc := &mcov1.KubeletConfig{}
					err := r.Get(context.TODO(), key, kc)
					Expect(err).ToNot(HaveOccurred())
					Expect(string(kc.Spec.KubeletConfig.Raw)).To(ContainSubstring(fmt.Sprintf(`"reservedSystemCPUs":"%s"`, string(*profile.Spec.CPU.Reserved))))

					By("Verifying Tuned update for isolated")
					key = types.NamespacedName{
						Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
						Namespace: components.NamespaceNodeTuningOperator,
					}
					t := &tunedv1.Tuned{}
					err = r.Get(context.TODO(), key, t)
					Expect(err).ToNot(HaveOccurred())
					Expect(*t.Spec.Profile[0].Data).To(ContainSubstring("isolated_cores=" + string(*profile.Spec.CPU.Isolated)))
				})

				It("should add isolcpus with managed_irq flag to tuned profile when balanced set to true", func() {
					reserved := performancev2.CPUSet("0-1")
					isolated := performancev2.CPUSet("2-3")
					profile.Spec.CPU = &performancev2.CPU{
						Reserved:        &reserved,
						Isolated:        &isolated,
						BalanceIsolated: pointer.Bool(true),
					}

					r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator)

					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					key := types.NamespacedName{
						Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
						Namespace: components.NamespaceNodeTuningOperator,
					}
					t := &tunedv1.Tuned{}
					err := r.Get(context.TODO(), key, t)
					Expect(err).ToNot(HaveOccurred())
					cmdlineRealtimeWithoutCPUBalancing := regexp.MustCompile(`\s*cmdline_isolation=\+\s*isolcpus=managed_irq\s*`)
					Expect(cmdlineRealtimeWithoutCPUBalancing.MatchString(*t.Spec.Profile[0].Data)).To(BeTrue())
				})

				It("should add isolcpus with domain,managed_irq flags to tuned profile when balanced set to false", func() {
					reserved := performancev2.CPUSet("0-1")
					isolated := performancev2.CPUSet("2-3")
					profile.Spec.CPU = &performancev2.CPU{
						Reserved:        &reserved,
						Isolated:        &isolated,
						BalanceIsolated: pointer.Bool(false),
					}

					r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator)

					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					key := types.NamespacedName{
						Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
						Namespace: components.NamespaceNodeTuningOperator,
					}
					t := &tunedv1.Tuned{}
					err := r.Get(context.TODO(), key, t)
					Expect(err).ToNot(HaveOccurred())
					cmdlineRealtimeWithoutCPUBalancing := regexp.MustCompile(`\s*cmdline_isolation=\+\s*isolcpus=domain,managed_irq,\s*`)
					Expect(cmdlineRealtimeWithoutCPUBalancing.MatchString(*t.Spec.Profile[0].Data)).To(BeTrue())
				})

				DescribeTable("should update MC when Hugepages params change without node added",
					func(size performancev2.HugePageSize) {
						profile.Spec.HugePages = &performancev2.HugePages{
							DefaultHugePagesSize: &size,
							Pages: []performancev2.HugePage{
								{
									Count: 8,
									Size:  size,
								},
							},
						}

						r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator)
						Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

						By("Verifying Tuned profile update")
						key := types.NamespacedName{
							Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
							Namespace: components.NamespaceNodeTuningOperator,
						}
						t := &tunedv1.Tuned{}
						err := r.Get(context.TODO(), key, t)
						Expect(err).ToNot(HaveOccurred())
						expectedCmdline := fmt.Sprintf(`\s*cmdline_hugepages=\+\s*default_hugepagesz=%s\s+hugepagesz=%s\s+hugepages=8\s*`, size, size)
						cmdlineHugepages := regexp.MustCompile(expectedCmdline)
						Expect(cmdlineHugepages.MatchString(*t.Spec.Profile[0].Data)).To(BeTrue())
					},
					Entry("Hugepages size 2M", performancev2.HugePageSize("2M")),
					Entry("Hugepages size 32M", performancev2.HugePageSize("32M")),
					Entry("Hugepages size 512M", performancev2.HugePageSize("512M")),
					Entry("Hugepages size 1G", performancev2.HugePageSize("1G")),
				)

				DescribeTable("should update Tuned when Hugepages params change with node added",
					func(size performancev2.HugePageSize) {
						profile.Spec.HugePages = &performancev2.HugePages{
							DefaultHugePagesSize: &size,
							Pages: []performancev2.HugePage{
								{
									Count: 8,
									Size:  size,
									Node:  pointer.Int32(0),
								},
							},
						}

						r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator)
						Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

						By("Verifying Tuned update")
						key := types.NamespacedName{
							Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
							Namespace: components.NamespaceNodeTuningOperator,
						}
						t := &tunedv1.Tuned{}
						err := r.Get(context.TODO(), key, t)
						Expect(err).ToNot(HaveOccurred())
						cmdlineHugepages := regexp.MustCompile(`\s*cmdline_hugepages=\+\s*`)
						Expect(cmdlineHugepages.MatchString(*t.Spec.Profile[0].Data)).To(BeTrue())

						By("Verifying MC update")
						key = types.NamespacedName{
							Name:      machineconfig.GetMachineConfigName(profile.Name),
							Namespace: metav1.NamespaceNone,
						}
						mc := &mcov1.MachineConfig{}
						err = r.Get(context.TODO(), key, mc)
						Expect(err).ToNot(HaveOccurred())

						config := &igntypes.Config{}
						err = json.Unmarshal(mc.Spec.Config.Raw, config)
						Expect(err).ToNot(HaveOccurred())

						hugepageSizeKiloBytes, err := machineconfig.GetHugepagesSizeKilobytes(size)
						Expect(err).ToNot(HaveOccurred())

						Expect(config.Systemd.Units).To(ContainElement(MatchFields(IgnoreMissing|IgnoreExtras, Fields{
							"Contents": And(
								ContainSubstring("Description=Hugepages"),
								ContainSubstring("Environment=HUGEPAGES_COUNT=8"),
								ContainSubstring(fmt.Sprintf("Environment=HUGEPAGES_SIZE=%s", hugepageSizeKiloBytes)),
								ContainSubstring("Environment=NUMA_NODE=0"),
							),
						})))
					},

					Entry("Hugepages size 2M", performancev2.HugePageSize("2M")),
					Entry("Hugepages size 32M", performancev2.HugePageSize("32M")),
					Entry("Hugepages size 512M", performancev2.HugePageSize("512M")),
					Entry("Hugepages size 1G", performancev2.HugePageSize("1G")),
				)

				It("should update status with generated tuned", func() {
					r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator)
					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))
					key := types.NamespacedName{
						Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
						Namespace: components.NamespaceNodeTuningOperator,
					}
					t := &tunedv1.Tuned{}
					err := r.Get(context.TODO(), key, t)
					Expect(err).ToNot(HaveOccurred())
					tunedNamespacedName := namespacedName(t).String()
					updatedProfile := &performancev2.PerformanceProfile{}
					key = types.NamespacedName{
						Name:      profile.Name,
						Namespace: metav1.NamespaceNone,
					}
					Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())
					Expect(updatedProfile.Status.Tuned).NotTo(BeNil())
					Expect(*updatedProfile.Status.Tuned).To(Equal(tunedNamespacedName))
				})

				It("should update status with generated runtime class", func() {
					r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP, infra, clusterOperator)
					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					key := types.NamespacedName{
						Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
						Namespace: metav1.NamespaceAll,
					}
					runtimeClass := &nodev1.RuntimeClass{}
					err := r.Get(context.TODO(), key, runtimeClass)
					Expect(err).ToNot(HaveOccurred())

					updatedProfile := &performancev2.PerformanceProfile{}
					key = types.NamespacedName{
						Name:      profile.Name,
						Namespace: metav1.NamespaceAll,
					}
					Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())
					Expect(updatedProfile.Status.RuntimeClass).NotTo(BeNil())
					Expect(*updatedProfile.Status.RuntimeClass).To(Equal(runtimeClass.Name))
				})

				It("should update status when MCP is degraded", func() {
					mcpReason := "mcpReason"
					mcpMessage := "MCP message"

					mcp := &mcov1.MachineConfigPool{
						TypeMeta: metav1.TypeMeta{
							APIVersion: mcov1.GroupVersion.String(),
							Kind:       "MachineConfigPool",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "mcp-test",
							Labels: map[string]string{
								testutils.MachineConfigPoolLabelKey: testutils.MachineConfigPoolLabelValue,
							},
						},
						Spec: mcov1.MachineConfigPoolSpec{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"nodekey": "nodeValue"},
							},
							MachineConfigSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      testutils.MachineConfigLabelKey,
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{testutils.MachineConfigLabelValue},
									},
								},
							},
						},
						Status: mcov1.MachineConfigPoolStatus{
							Conditions: []mcov1.MachineConfigPoolCondition{
								{
									Type:    mcov1.MachineConfigPoolNodeDegraded,
									Status:  corev1.ConditionTrue,
									Reason:  mcpReason,
									Message: mcpMessage,
								},
							},
							Configuration: mcov1.MachineConfigPoolStatusConfiguration{
								ObjectReference: corev1.ObjectReference{
									Name: "test",
								},
							},
						},
					}

					r := newFakeReconciler(profile, mc, kc, tunedPerformance, mcp, infra, clusterOperator)

					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					updatedProfile := &performancev2.PerformanceProfile{}
					key := types.NamespacedName{
						Name:      profile.Name,
						Namespace: metav1.NamespaceNone,
					}
					Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())

					// verify performance profile status
					Expect(len(updatedProfile.Status.Conditions)).To(Equal(4))

					// verify profile conditions
					degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
					Expect(degradedCondition).ToNot(BeNil())
					Expect(degradedCondition.Status).To(Equal(corev1.ConditionTrue))
					Expect(degradedCondition.Reason).To(Equal(status.ConditionReasonMCPDegraded))
					Expect(degradedCondition.Message).To(ContainSubstring(mcpMessage))
				})

				It("should update status when TunedProfile is degraded", func() {
					tunedReason := "tunedReason"
					tunedMessage := "Tuned message"

					tuned := &tunedv1.Profile{
						ObjectMeta: metav1.ObjectMeta{
							Name: "tuned-profile-test",
						},
						Status: tunedv1.ProfileStatus{
							Conditions: []tunedv1.ProfileStatusCondition{
								{
									Type:    tunedv1.TunedDegraded,
									Status:  corev1.ConditionTrue,
									Reason:  tunedReason,
									Message: tunedMessage,
								},
								{
									Type:    tunedv1.TunedProfileApplied,
									Status:  corev1.ConditionFalse,
									Reason:  tunedReason,
									Message: tunedMessage,
								},
							},
						},
					}

					nodes := &corev1.NodeList{
						Items: []corev1.Node{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "tuned-profile-test",
									Labels: map[string]string{
										"nodekey": "nodeValue",
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "tuned-profile-test2",
								},
							},
						},
					}

					r := newFakeReconciler(profile, mc, kc, tunedPerformance, tuned, nodes, profileMCP, infra, clusterOperator)

					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					updatedProfile := &performancev2.PerformanceProfile{}
					key := types.NamespacedName{
						Name:      profile.Name,
						Namespace: metav1.NamespaceNone,
					}
					Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())

					// verify performance profile status
					Expect(len(updatedProfile.Status.Conditions)).To(Equal(4))

					// verify profile conditions
					degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
					Expect(degradedCondition).ToNot(BeNil())
					Expect(degradedCondition.Status).To(Equal(corev1.ConditionTrue))
					Expect(degradedCondition.Reason).To(Equal(status.ConditionReasonTunedDegraded))
					Expect(degradedCondition.Message).To(ContainSubstring(tunedMessage))
				})
			})

			When("the provided machine config labels are different from one specified under the machine config pool", func() {
				BeforeEach(skipForHypershift)
				It("should move the performance profile to the degraded state", func() {
					profileMCP.Spec.MachineConfigSelector = &metav1.LabelSelector{
						MatchLabels: map[string]string{"wrongKey": "bad"},
					}
					r := newFakeReconciler(profile, profileMCP, infra, clusterOperator)
					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					updatedProfile := &performancev2.PerformanceProfile{}
					key := types.NamespacedName{
						Name:      profile.Name,
						Namespace: metav1.NamespaceNone,
					}
					Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())

					// verify performance profile status
					Expect(len(updatedProfile.Status.Conditions)).To(Equal(4))

					// verify profile conditions
					degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
					Expect(degradedCondition).ToNot(BeNil())
					Expect(degradedCondition.Status).To(Equal(corev1.ConditionTrue))
					Expect(degradedCondition.Reason).To(Equal(status.ConditionBadMachineConfigLabels))
					Expect(degradedCondition.Message).To(ContainSubstring("provided via profile.spec.machineConfigLabel do not match the MachineConfigPool"))
				})
			})

			When("the generated machine config labels are different from one specified under the machine config pool", func() {
				BeforeEach(skipForHypershift)
				It("should move the performance profile to the degraded state", func() {
					profileMCP.Spec.MachineConfigSelector = &metav1.LabelSelector{
						MatchLabels: map[string]string{"wrongKey": "bad"},
					}
					profile.Spec.MachineConfigLabel = nil
					r := newFakeReconciler(profile, profileMCP, infra, clusterOperator)
					Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

					updatedProfile := &performancev2.PerformanceProfile{}
					key := types.NamespacedName{
						Name:      profile.Name,
						Namespace: metav1.NamespaceNone,
					}
					Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())

					// verify performance profile status
					Expect(len(updatedProfile.Status.Conditions)).To(Equal(4))

					// verify profile conditions
					degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
					Expect(degradedCondition).ToNot(BeNil())
					Expect(degradedCondition.Status).To(Equal(corev1.ConditionTrue))
					Expect(degradedCondition.Reason).To(Equal(status.ConditionBadMachineConfigLabels))
					Expect(degradedCondition.Message).To(ContainSubstring("generated from the profile.spec.nodeSelector"))
				})
			})
		})

		Context("with the kubelet.experimental annotation set", func() {
			BeforeEach(skipForHypershift)
			It("should create all resources on first reconcile loop", func() {
				prof := profile.DeepCopy()
				prof.Annotations = map[string]string{
					"kubeletconfig.experimental": `{"systemReserved": {"memory": "256Mi"}, "kubeReserved": {"memory": "256Mi"}}`,
				}
				r := newFakeReconciler(prof, profileMCP, infra, clusterOperator)

				Expect(reconcileTimes(r, request, 2)).To(Equal(reconcile.Result{}))

				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
					Namespace: metav1.NamespaceNone,
				}

				kc := &mcov1.KubeletConfig{}
				err := r.Get(context.TODO(), key, kc)
				Expect(err).ToNot(HaveOccurred())

				kubeletConfigString := string(kc.Spec.KubeletConfig.Raw)
				Expect(kubeletConfigString).To(ContainSubstring(`"kubeReserved":{"memory":"256Mi"}`))
				Expect(kubeletConfigString).To(ContainSubstring(`"systemReserved":{"memory":"256Mi"}`))
			})
		})

		Context("with profile with deletion timestamp", func() {
			BeforeEach(func() {
				skipForHypershift()
				profile.DeletionTimestamp = &metav1.Time{
					Time: time.Now(),
				}
				profile.Finalizers = append(profile.Finalizers, finalizer)
			})

			It("should remove all components and remove the finalizer on first reconcile loop", func() {
				mc, err := machineconfig.New(profile, &components.MachineConfigOptions{})
				Expect(err).ToNot(HaveOccurred())

				mcpSelectorKey, mcpSelectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
				kc, err := kubeletconfig.New(profile, &components.KubeletConfigOptions{MachineConfigPoolSelector: map[string]string{mcpSelectorKey: mcpSelectorValue}})
				Expect(err).ToNot(HaveOccurred())

				tunedPerformance, err := tuned.NewNodePerformance(profile)
				Expect(err).ToNot(HaveOccurred())

				runtimeClass := runtimeclass.New(profile, machineconfig.HighPerformanceRuntime)

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP, infra, clusterOperator)
				result, err := r.Reconcile(context.TODO(), request)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				// verify that controller deleted all components
				name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				key := types.NamespacedName{
					Name:      name,
					Namespace: metav1.NamespaceNone,
				}

				// verify MachineConfig deletion
				err = r.Get(context.TODO(), key, mc)
				Expect(errors.IsNotFound(err)).To(Equal(true))

				// verify KubeletConfig deletion
				err = r.Get(context.TODO(), key, kc)
				Expect(errors.IsNotFound(err)).To(Equal(true))

				// verify RuntimeClass deletion
				err = r.Get(context.TODO(), key, runtimeClass)
				Expect(errors.IsNotFound(err)).To(Equal(true))

				// verify tuned real-time kernel deletion
				key.Name = components.GetComponentName(profile.Name, components.ProfileNamePerformance)
				key.Namespace = components.NamespaceNodeTuningOperator
				err = r.Get(context.TODO(), key, tunedPerformance)
				Expect(errors.IsNotFound(err)).To(Equal(true))

				// verify profile deletion
				key.Name = profile.Name
				key.Namespace = metav1.NamespaceNone
				updatedProfile := &performancev2.PerformanceProfile{}
				err = r.Get(context.TODO(), key, updatedProfile)
				Expect(errors.IsNotFound(err)).To(Equal(true))
			})
		})

		Context("with infrastructure cpuPartitioning", func() {
			BeforeEach(func() {
				skipForHypershift()
				infra = testutils.NewInfraResource(true)
			})

			It("should contain cpu partitioning files in machine config", func() {
				mc, err := machineconfig.New(profile, &components.MachineConfigOptions{PinningMode: &infra.Status.CPUPartitioning})
				Expect(err).ToNot(HaveOccurred())
				r := newFakeReconciler(profile, profileMCP, mc, infra, clusterOperator)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				By("Verifying MC update")
				key := types.NamespacedName{
					Name:      machineconfig.GetMachineConfigName(profile.Name),
					Namespace: metav1.NamespaceNone,
				}
				mc = &mcov1.MachineConfig{}
				err = r.Get(context.TODO(), key, mc)
				Expect(err).ToNot(HaveOccurred())

				config := &igntypes.Config{}
				err = json.Unmarshal(mc.Spec.Config.Raw, config)
				Expect(err).ToNot(HaveOccurred())

				mode := 420
				containFiles := []igntypes.File{
					{
						Node: igntypes.Node{
							Path:  "/etc/kubernetes/openshift-workload-pinning",
							Group: &igntypes.NodeGroup{},
							User:  &igntypes.NodeUser{},
						},
						FileEmbedded1: igntypes.FileEmbedded1{
							Contents: igntypes.FileContents{
								Verification: igntypes.Verification{},
								Source:       "data:text/plain;charset=utf-8;base64,CnsKICAibWFuYWdlbWVudCI6IHsKICAgICJjcHVzZXQiOiAiMC0zIgogIH0KfQo=",
							},
							Mode: &mode,
						},
					},
					{
						Node: igntypes.Node{
							Path:  "/etc/crio/crio.conf.d/99-workload-pinning.conf",
							Group: &igntypes.NodeGroup{},
							User:  &igntypes.NodeUser{},
						},
						FileEmbedded1: igntypes.FileEmbedded1{
							Contents: igntypes.FileContents{
								Verification: igntypes.Verification{},
								Source:       "data:text/plain;charset=utf-8;base64,CltjcmlvLnJ1bnRpbWUud29ya2xvYWRzLm1hbmFnZW1lbnRdCmFjdGl2YXRpb25fYW5ub3RhdGlvbiA9ICJ0YXJnZXQud29ya2xvYWQub3BlbnNoaWZ0LmlvL21hbmFnZW1lbnQiCmFubm90YXRpb25fcHJlZml4ID0gInJlc291cmNlcy53b3JrbG9hZC5vcGVuc2hpZnQuaW8iCnJlc291cmNlcyA9IHsgImNwdXNoYXJlcyIgPSAwLCAiY3B1c2V0IiA9ICIwLTMiIH0K",
							},
							Mode: &mode,
						},
					},
				}

				Expect(config.Storage.Files).To(ContainElements(containFiles))
			})
		})

		It("should map machine config pool to the performance profile", func() {
			skipForHypershift()
			mcp := &mcov1.MachineConfigPool{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mcov1.GroupVersion.String(),
					Kind:       "MachineConfigPool",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "mcp-test",
				},
				Spec: mcov1.MachineConfigPoolSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"nodekey": "nodeValue"},
					},
					MachineConfigSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      testutils.MachineConfigLabelKey,
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{testutils.MachineConfigLabelValue},
							},
						},
					},
				},
			}
			r := newFakeReconciler(profile, mcp)
			requests := r.mcpToPerformanceProfile(context.TODO(), mcp)
			Expect(requests).NotTo(BeEmpty())
			Expect(requests[0].Name).To(Equal(profile.Name))
		})

		Context("with Mixed CPUs enabled", func() {
			BeforeEach(skipForHypershift)
			It("should append the shared cpus to Kubelet config", func() {
				mcpSelectorKey, mcpSelectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
				kc, err := kubeletconfig.New(profile, &components.KubeletConfigOptions{MachineConfigPoolSelector: map[string]string{mcpSelectorKey: mcpSelectorValue}, MixedCPUsEnabled: true})
				Expect(err).ToNot(HaveOccurred())

				r := newFakeReconciler(profile, profileMCP, kc, infra, clusterOperator)
				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				By("verify Kubelet config")
				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
					Namespace: metav1.NamespaceNone,
				}
				err = r.Get(context.TODO(), key, kc)
				Expect(err).ToNot(HaveOccurred())

				reserved, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
				Expect(err).ToNot(HaveOccurred())
				shared, err := cpuset.Parse(string(*profile.Spec.CPU.Shared))
				Expect(err).ToNot(HaveOccurred())

				k8sKC := &kubeletconfigv1beta1.KubeletConfiguration{}
				err = json.Unmarshal(kc.Spec.KubeletConfig.Raw, k8sKC)
				Expect(err).ToNot(HaveOccurred())
				k8sReserved, err := cpuset.Parse(k8sKC.ReservedSystemCPUs)
				Expect(err).ToNot(HaveOccurred())
				Expect(k8sReserved.Equals(reserved.Union(shared))).To(BeTrue(),
					"Kubelet ReservedSystemCPUs in not equal to reserved + shared; ReservedSystemCPUs=%q shared=%q reserved=%q", k8sReserved.String(), shared.String(), reserved.String())
			})
			It("should contains all configuration files under Machine Config", func() {
				mc, err := machineconfig.New(profile, &components.MachineConfigOptions{PinningMode: &infra.Status.CPUPartitioning, MixedCPUsEnabled: true})
				Expect(err).ToNot(HaveOccurred())
				r := newFakeReconciler(profile, profileMCP, mc, infra, clusterOperator)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				By("Verifying MC update")
				key := types.NamespacedName{
					Name:      machineconfig.GetMachineConfigName(profile.Name),
					Namespace: metav1.NamespaceNone,
				}
				mc = &mcov1.MachineConfig{}
				err = r.Get(context.TODO(), key, mc)
				Expect(err).ToNot(HaveOccurred())

				config := &igntypes.Config{}
				err = json.Unmarshal(mc.Spec.Config.Raw, config)
				Expect(err).ToNot(HaveOccurred())

				var containFiles = []igntypes.File{
					{
						Node: igntypes.Node{
							Path:  "/etc/kubernetes/openshift-workload-mixed-cpus",
							Group: &igntypes.NodeGroup{},
							User:  &igntypes.NodeUser{},
						},
						FileEmbedded1: igntypes.FileEmbedded1{
							Contents: igntypes.FileContents{
								Verification: igntypes.Verification{},
								Source:       "data:text/plain;charset=utf-8;base64,CnsKICAic2hhcmVkX2NwdXMiOiB7CiAgICAgImNvbnRhaW5lcnNfbGltaXQiOiAyNTYKICB9Cn0=",
							},
							Mode: pointer.Int(0644),
						},
					},
				}
				Expect(config.Storage.Files).To(ContainElements(containFiles))
			})

		})
	},
		Entry("OpenShift Platform", newFakeReconciler, "foreground-deletion", false),
		Entry("HyperShift Platform", newFakeReconcilerForHypershift, "hypershift.openshift.io/foreground-deletion", true),
	)
})

func reconcileTimes(reconciler *PerformanceProfileReconciler, request reconcile.Request, times int) reconcile.Result {
	GinkgoHelper()
	var result reconcile.Result
	var err error
	for i := 0; i < times; i++ {
		result, err = reconciler.Reconcile(context.TODO(), request)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}
	return result
}

func MCPInterceptor() interceptor.Funcs {
	GinkgoHelper()
	generationCounter := int64(0)

	mutateMCP := func(mcp *mcov1.MachineConfigPool) {
		mcp.SetGeneration(generationCounter)
	}

	return interceptor.Funcs{
		Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, isNodeUpdate := obj.(*apiconfigv1.Node); isNodeUpdate {
				generationCounter++
			}
			return client.Update(ctx, obj, opts...)
		},
		Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			mcp, isMcp := obj.(*mcov1.MachineConfigPool)
			err := client.Get(ctx, key, obj)
			if err == nil && isMcp {
				mutateMCP(mcp)
			}
			return err
		},
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			mcpList, isMcpList := list.(*mcov1.MachineConfigPoolList)
			err := client.List(ctx, list, opts...)
			if err == nil && isMcpList {
				for i := range mcpList.Items {
					mutateMCP(&mcpList.Items[i])
				}
			}
			return err
		},
	}
}

// newFakeReconciler returns a new reconcile.Reconciler with a fake client
func newFakeReconciler(instance client.Object, initObjects ...runtime.Object) *PerformanceProfileReconciler {
	GinkgoHelper()
	// we need to add the profile using the `WithStatusSubresource` function
	// because we're updating its status during the reconciliation loop
	initObjects = append(initObjects, instance)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(instance).WithRuntimeObjects(initObjects...).WithInterceptorFuncs(MCPInterceptor()).Build()
	fakeRecorder := record.NewFakeRecorder(10)
	fakeFeatureGateAccessor := featuregates.NewHardcodedFeatureGateAccessForTesting(nil, []configv1.FeatureGateName{apifeatures.FeatureGateMixedCPUsAllocation}, make(chan struct{}), nil)
	fg, _ := fakeFeatureGateAccessor.CurrentFeatureGates()
	return &PerformanceProfileReconciler{
		Client:            fakeClient,
		ManagementClient:  fakeClient,
		Recorder:          fakeRecorder,
		FeatureGate:       fg,
		ComponentsHandler: handler.NewHandler(fakeClient, scheme.Scheme),
		StatusWriter:      status.NewWriter(fakeClient),
	}
}

func newFakeReconcilerForHypershift(instance client.Object, initObjects ...runtime.Object) *PerformanceProfileReconciler {
	GinkgoHelper()
	mngClusterObjects, hostedClusterObjects := adaptObjectsForHypershift(instance, initObjects...)
	mngClusterObjects = append(mngClusterObjects, instance)
	fakeMngClient := hypershift.NewControlPlaneClient(fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(instance).WithRuntimeObjects(mngClusterObjects...).Build(),
		hostedControlPlaneNamespace)
	fakeHostedClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(hostedClusterObjects...).Build()
	fakeRecorder := record.NewFakeRecorder(10)
	fakeFeatureGateAccessor := featuregates.NewHardcodedFeatureGateAccessForTesting(nil, []configv1.FeatureGateName{apifeatures.FeatureGateMixedCPUsAllocation}, make(chan struct{}), nil)
	fg, _ := fakeFeatureGateAccessor.CurrentFeatureGates()
	return &PerformanceProfileReconciler{
		Client:            fakeHostedClient,
		ManagementClient:  fakeMngClient,
		Recorder:          fakeRecorder,
		FeatureGate:       fg,
		ComponentsHandler: hcpcomponents.NewHandler(fakeMngClient, fakeHostedClient, scheme.Scheme),
		StatusWriter:      hcpstatus.NewWriter(fakeMngClient, fakeHostedClient, scheme.Scheme),
	}
}

func adaptObjectsForHypershift(instance client.Object, initObjects ...runtime.Object) ([]runtime.Object, []runtime.Object) {
	GinkgoHelper()
	profileCM, ok := instance.(*corev1.ConfigMap)
	if !ok {
		Fail(fmt.Sprintf("expected *corev1.ConfigMap, got %T", instance))
	}
	var mngClusterObjects, hostedClusterObjects []runtime.Object
	for _, obj := range initObjects {
		switch obj.(type) {
		case *tunedv1.Tuned:
			cm, err := hcpcomponents.EncapsulateObjInConfigMap(scheme.Scheme, profileCM, obj.(client.Object), performanceProfileName, hypershiftconsts.TuningKey, map[string]string{hypershiftconsts.ControllerGeneratedTunedConfigMapLabel: "true"})
			if err != nil {
				klog.Fatal(err)
			}
			mngClusterObjects = append(mngClusterObjects, cm)
		case *mcov1.MachineConfig, *mcov1.KubeletConfig:
			cm, err := hcpcomponents.EncapsulateObjInConfigMap(scheme.Scheme, profileCM, obj.(client.Object), performanceProfileName, hypershiftconsts.ConfigKey, map[string]string{hypershiftconsts.NTOGeneratedMachineConfigLabel: "true"})
			if err != nil {
				klog.Fatal(err)
			}
			mngClusterObjects = append(mngClusterObjects, cm)
		case *apiconfigv1.Infrastructure, *apiconfigv1.ClusterOperator:
			hostedClusterObjects = append(hostedClusterObjects, obj)
		case *mcov1.MachineConfigPool:
			continue
		default:
			Fail(fmt.Sprintf("object type %T not supported", obj))
		}
	}
	return mngClusterObjects, hostedClusterObjects
}

func encapsulateProfileInConfigMap(profile *performancev2.PerformanceProfile) *corev1.ConfigMap {
	GinkgoHelper()
	encodedObj, err := hypershift.EncodeManifest(profile, scheme.Scheme)
	if err != nil {
		Fail(fmt.Sprintf("failed to encode manifest %v", err))
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pp-test-config",
			Namespace: hostedControlPlaneNamespace,
			Annotations: map[string]string{
				"hypershift.openshift.io/nodePool": "nodepool-test",
			},
		},
		Data: map[string]string{
			"tuning": string(encodedObj),
		},
	}
}

func skipForHypershift() {
	if _, ok := os.LookupEnv("HYPERSHIFT"); ok {
		Skip("This test is not applicable for hypershift, skipping...")
	}
}
