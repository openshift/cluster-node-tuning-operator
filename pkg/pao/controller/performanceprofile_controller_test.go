package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	igntypes "github.com/coreos/ignition/config/v2_2/types"
	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/kubeletconfig"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/machineconfig"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/runtimeclass"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/tuned"
	testutils "github.com/openshift-kni/performance-addon-operators/pkg/utils/testing"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	corev1 "k8s.io/api/core/v1"
	nodev1beta1 "k8s.io/api/node/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Controller", func() {
	var request reconcile.Request
	var profile *performancev2.PerformanceProfile
	var profileMCP *mcov1.MachineConfigPool

	BeforeEach(func() {
		profileMCP = testutils.NewProfileMCP()
		profile = testutils.NewPerformanceProfile("test")
		request = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: metav1.NamespaceNone,
				Name:      profile.Name,
			},
		}
	})

	It("should add finalizer to the performance profile", func() {
		r := newFakeReconciler(profile, profileMCP)

		Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

		updatedProfile := &performancev2.PerformanceProfile{}
		key := types.NamespacedName{
			Name:      profile.Name,
			Namespace: metav1.NamespaceNone,
		}
		Expect(r.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())
		Expect(hasFinalizer(updatedProfile, finalizer)).To(Equal(true))
	})

	Context("with profile with finalizer", func() {
		BeforeEach(func() {
			profile.Finalizers = append(profile.Finalizers, finalizer)
		})

		It("should create all resources on first reconcile loop", func() {
			r := newFakeReconciler(profile, profileMCP)

			Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

			key := types.NamespacedName{
				Name:      machineconfig.GetMachineConfigName(profile),
				Namespace: metav1.NamespaceNone,
			}

			// verify MachineConfig creation
			mc := &mcov1.MachineConfig{}
			err := r.Get(context.TODO(), key, mc)
			Expect(err).ToNot(HaveOccurred())

			key = types.NamespacedName{
				Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
				Namespace: metav1.NamespaceNone,
			}

			// verify KubeletConfig creation
			kc := &mcov1.KubeletConfig{}
			err = r.Get(context.TODO(), key, kc)
			Expect(err).ToNot(HaveOccurred())

			// verify RuntimeClass creation
			runtimeClass := &nodev1beta1.RuntimeClass{}
			err = r.Get(context.TODO(), key, runtimeClass)
			Expect(err).ToNot(HaveOccurred())

			// verify tuned performance creation
			tunedPerformance := &tunedv1.Tuned{}
			key.Name = components.GetComponentName(profile.Name, components.ProfileNamePerformance)
			key.Namespace = components.NamespaceNodeTuningOperator
			err = r.Get(context.TODO(), key, tunedPerformance)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should create event on the second reconcile loop", func() {
			r := newFakeReconciler(profile, profileMCP)

			Expect(reconcileTimes(r, request, 2)).To(Equal(reconcile.Result{}))

			// verify creation event
			fakeRecorder, ok := r.Recorder.(*record.FakeRecorder)
			Expect(ok).To(BeTrue())
			event := <-fakeRecorder.Events
			Expect(event).To(ContainSubstring("Creation succeeded"))
		})

		It("should update the profile status", func() {
			r := newFakeReconciler(profile, profileMCP)

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
			progressingCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionProgressing)
			Expect(progressingCondition).ToNot(BeNil())
			Expect(progressingCondition.Status).To(Equal(corev1.ConditionFalse))
			availableCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionAvailable)
			Expect(availableCondition).ToNot(BeNil())
			Expect(availableCondition.Status).To(Equal(corev1.ConditionTrue))
		})

		It("should promote kubelet config failure condition", func() {
			r := newFakeReconciler(profile, profileMCP)
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
			Expect(degradedCondition.Reason).To(Equal(conditionKubeletFailed))
		})

		It("should not promote old failure condition", func() {
			r := newFakeReconciler(profile, profileMCP)
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
			r := newFakeReconciler(profile, tunedOutdatedA, tunedOutdatedB, profileMCP)

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
			r := newFakeReconciler(profile, profileMCP)

			Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

			name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
			key := types.NamespacedName{
				Name:      name,
				Namespace: metav1.NamespaceNone,
			}

			// verify MachineConfig wasn't created
			mc := &mcov1.MachineConfig{}
			err := r.Get(context.TODO(), key, mc)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// verify that KubeletConfig wasn't created
			kc := &mcov1.KubeletConfig{}
			err = r.Get(context.TODO(), key, kc)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// verify no machine config pool was created
			mcp := &mcov1.MachineConfigPool{}
			err = r.Get(context.TODO(), key, mcp)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// verify tuned Performance wasn't created
			tunedPerformance := &tunedv1.Tuned{}
			key.Name = components.ProfileNamePerformance
			key.Namespace = components.NamespaceNodeTuningOperator
			err = r.Get(context.TODO(), key, tunedPerformance)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// verify that no RuntimeClass was created
			runtimeClass := &nodev1beta1.RuntimeClass{}
			err = r.Get(context.TODO(), key, runtimeClass)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		Context("when all components exist", func() {
			var mc *mcov1.MachineConfig
			var kc *mcov1.KubeletConfig
			var tunedPerformance *tunedv1.Tuned
			var runtimeClass *nodev1beta1.RuntimeClass

			BeforeEach(func() {
				var err error

				mc, err = machineconfig.New(profile)
				Expect(err).ToNot(HaveOccurred())

				mcpSelectorKey, mcpSelectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
				kc, err = kubeletconfig.New(profile, map[string]string{mcpSelectorKey: mcpSelectorValue})
				Expect(err).ToNot(HaveOccurred())

				tunedPerformance, err = tuned.NewNodePerformance(profile)
				Expect(err).ToNot(HaveOccurred())

				runtimeClass = runtimeclass.New(profile, machineconfig.HighPerformanceRuntime)
			})

			It("should not record new create event", func() {
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				// verify that no creation event created
				fakeRecorder, ok := r.Recorder.(*record.FakeRecorder)
				Expect(ok).To(BeTrue())

				select {
				case _ = <-fakeRecorder.Events:
					Fail("the recorder should not have new events")
				default:
				}
			})

			It("should update MC when RT kernel gets disabled", func() {
				profile.Spec.RealTimeKernel.Enabled = pointer.BoolPtr(false)
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				key := types.NamespacedName{
					Name:      machineconfig.GetMachineConfigName(profile),
					Namespace: metav1.NamespaceNone,
				}

				// verify MachineConfig update
				mc := &mcov1.MachineConfig{}
				err := r.Get(context.TODO(), key, mc)
				Expect(err).ToNot(HaveOccurred())

				Expect(mc.Spec.KernelType).To(Equal(machineconfig.MCKernelDefault))
			})

			It("should update MC, KC and Tuned when CPU params change", func() {
				reserved := performancev2.CPUSet("0-1")
				isolated := performancev2.CPUSet("2-3")
				profile.Spec.CPU = &performancev2.CPU{
					Reserved: &reserved,
					Isolated: &isolated,
				}

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP)

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
					BalanceIsolated: pointer.BoolPtr(true),
				}

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
					Namespace: components.NamespaceNodeTuningOperator,
				}
				t := &tunedv1.Tuned{}
				err := r.Get(context.TODO(), key, t)
				Expect(err).ToNot(HaveOccurred())
				cmdlineRealtimeWithoutCPUBalancing := regexp.MustCompile(`\s*cmdline_realtime=\+\s*tsc=nowatchdog\s+intel_iommu=on\s+iommu=pt\s+isolcpus=managed_irq\s*`)
				Expect(cmdlineRealtimeWithoutCPUBalancing.MatchString(*t.Spec.Profile[0].Data)).To(BeTrue())
			})

			It("should add isolcpus with domain,managed_irq flags to tuned profile when balanced set to false", func() {
				reserved := performancev2.CPUSet("0-1")
				isolated := performancev2.CPUSet("2-3")
				profile.Spec.CPU = &performancev2.CPU{
					Reserved:        &reserved,
					Isolated:        &isolated,
					BalanceIsolated: pointer.BoolPtr(false),
				}

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
					Namespace: components.NamespaceNodeTuningOperator,
				}
				t := &tunedv1.Tuned{}
				err := r.Get(context.TODO(), key, t)
				Expect(err).ToNot(HaveOccurred())
				cmdlineRealtimeWithoutCPUBalancing := regexp.MustCompile(`\s*cmdline_realtime=\+\s*tsc=nowatchdog\s+intel_iommu=on\s+iommu=pt\s+isolcpus=domain,managed_irq,\s*`)
				Expect(cmdlineRealtimeWithoutCPUBalancing.MatchString(*t.Spec.Profile[0].Data)).To(BeTrue())
			})

			It("should update MC when Hugepages params change without node added", func() {
				size := performancev2.HugePageSize("2M")
				profile.Spec.HugePages = &performancev2.HugePages{
					DefaultHugePagesSize: &size,
					Pages: []performancev2.HugePage{
						{
							Count: 8,
							Size:  size,
						},
					},
				}

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP)

				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				By("Verifying Tuned profile update")
				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
					Namespace: components.NamespaceNodeTuningOperator,
				}
				t := &tunedv1.Tuned{}
				err := r.Get(context.TODO(), key, t)
				Expect(err).ToNot(HaveOccurred())
				cmdlineHugepages := regexp.MustCompile(`\s*cmdline_hugepages=\+\s*default_hugepagesz=2M\s+hugepagesz=2M\s+hugepages=8\s*`)
				Expect(cmdlineHugepages.MatchString(*t.Spec.Profile[0].Data)).To(BeTrue())
			})

			It("should update Tuned when Hugepages params change with node added", func() {
				size := performancev2.HugePageSize("2M")
				profile.Spec.HugePages = &performancev2.HugePages{
					DefaultHugePagesSize: &size,
					Pages: []performancev2.HugePage{
						{
							Count: 8,
							Size:  size,
							Node:  pointer.Int32Ptr(0),
						},
					},
				}

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP)

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
					Name:      machineconfig.GetMachineConfigName(profile),
					Namespace: metav1.NamespaceNone,
				}
				mc := &mcov1.MachineConfig{}
				err = r.Get(context.TODO(), key, mc)
				Expect(err).ToNot(HaveOccurred())

				config := &igntypes.Config{}
				err = json.Unmarshal(mc.Spec.Config.Raw, config)
				Expect(err).ToNot(HaveOccurred())

				Expect(config.Systemd.Units).To(ContainElement(MatchFields(IgnoreMissing|IgnoreExtras, Fields{
					"Contents": And(
						ContainSubstring("Description=Hugepages"),
						ContainSubstring("Environment=HUGEPAGES_COUNT=8"),
						ContainSubstring("Environment=HUGEPAGES_SIZE=2048"),
						ContainSubstring("Environment=NUMA_NODE=0"),
					),
				})))

			})

			It("should update status with generated tuned", func() {
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP)
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
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP)
				Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
					Namespace: metav1.NamespaceAll,
				}
				runtimeClass := &nodev1beta1.RuntimeClass{}
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
					},
				}

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, mcp)

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
				Expect(degradedCondition.Reason).To(Equal(conditionReasonMCPDegraded))
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

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, tuned, nodes, profileMCP)

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
				Expect(degradedCondition.Reason).To(Equal(conditionReasonTunedDegraded))
				Expect(degradedCondition.Message).To(ContainSubstring(tunedMessage))
			})
		})

		When("the provided machine config labels are different from one specified under the machine config pool", func() {
			It("should move the performance profile to the degraded state", func() {
				profileMCP.Spec.MachineConfigSelector = &metav1.LabelSelector{
					MatchLabels: map[string]string{"wrongKey": "bad"},
				}
				r := newFakeReconciler(profile, profileMCP)
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
				Expect(degradedCondition.Reason).To(Equal(conditionBadMachineConfigLabels))
				Expect(degradedCondition.Message).To(ContainSubstring("provided via profile.spec.machineConfigLabel do not match the MachineConfigPool"))
			})
		})

		When("the generated machine config labels are different from one specified under the machine config pool", func() {
			It("should move the performance profile to the degraded state", func() {
				profileMCP.Spec.MachineConfigSelector = &metav1.LabelSelector{
					MatchLabels: map[string]string{"wrongKey": "bad"},
				}
				profile.Spec.MachineConfigLabel = nil
				r := newFakeReconciler(profile, profileMCP)
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
				Expect(degradedCondition.Reason).To(Equal(conditionBadMachineConfigLabels))
				Expect(degradedCondition.Message).To(ContainSubstring("generated from the profile.spec.nodeSelector"))
			})
		})
	})

	Context("with profile with deletion timestamp", func() {
		BeforeEach(func() {
			profile.DeletionTimestamp = &metav1.Time{
				Time: time.Now(),
			}
			profile.Finalizers = append(profile.Finalizers, finalizer)
		})

		It("should remove all components and remove the finalizer on first reconcile loop", func() {
			mc, err := machineconfig.New(profile)
			Expect(err).ToNot(HaveOccurred())

			mcpSelectorKey, mcpSelectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := kubeletconfig.New(profile, map[string]string{mcpSelectorKey: mcpSelectorValue})
			Expect(err).ToNot(HaveOccurred())

			tunedPerformance, err := tuned.NewNodePerformance(profile)
			Expect(err).ToNot(HaveOccurred())

			runtimeClass := runtimeclass.New(profile, machineconfig.HighPerformanceRuntime)

			r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP)
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

	It("should map machine config pool to the performance profile", func() {
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
		requests := r.mcpToPerformanceProfile(mcp)
		Expect(requests).NotTo(BeEmpty())
		Expect(requests[0].Name).To(Equal(profile.Name))
	})
})

func reconcileTimes(reconciler *PerformanceProfileReconciler, request reconcile.Request, times int) reconcile.Result {
	var result reconcile.Result
	var err error
	for i := 0; i < times; i++ {
		result, err = reconciler.Reconcile(context.TODO(), request)
		Expect(err).ToNot(HaveOccurred())
	}
	return result
}

// newFakeReconciler returns a new reconcile.Reconciler with a fake client
func newFakeReconciler(initObjects ...runtime.Object) *PerformanceProfileReconciler {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(initObjects...).Build()
	fakeRecorder := record.NewFakeRecorder(10)
	return &PerformanceProfileReconciler{
		Client:   fakeClient,
		Scheme:   scheme.Scheme,
		Recorder: fakeRecorder,
	}
}
