package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	igntypes "github.com/coreos/ignition/config/v2_2/types"
	apiconfigv1 "github.com/openshift/api/config/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/kubeletconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/runtimeclass"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/tuned"
	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/testing"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	kernelArgsv1 []string = []string{
		"systemd.unified_cgroup_hierarchy=0",
		"systemd.legacy_systemd_cgroup_controller=1",
	}
)

var _ = Describe("Controller", func() {
	var request reconcile.Request
	var profile *performancev2.PerformanceProfile
	var profileMC *mcov1.MachineConfig
	var profileMCP *mcov1.MachineConfigPool
	var infra *apiconfigv1.Infrastructure
	var clusterOperator *apiconfigv1.ClusterOperator
	var ctrcfg *mcov1.ContainerRuntimeConfig
	var nodeConfig *apiconfigv1.Node

	BeforeEach(func() {
		profileMC = testutils.NewProfileMachineConfig("test", kernelArgsv1)
		profileMCP = testutils.NewProfileMCP()
		profile = testutils.NewPerformanceProfile("test")
		infra = testutils.NewInfraResource(false)
		clusterOperator = testutils.NewClusterOperator()
		nodeConfig = testutils.NewNodeConfig(apiconfigv1.CgroupModeV1)
		request = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: metav1.NamespaceNone,
				Name:      profile.Name,
			},
		}
	})

	It("should add finalizer to the performance profile", func() {
		r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
			runtimeClass := &nodev1.RuntimeClass{}
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
			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

			Expect(reconcileTimes(r, request, 2)).To(Equal(reconcile.Result{}))

			// verify creation event
			fakeRecorder, ok := r.Recorder.(*record.FakeRecorder)
			Expect(ok).To(BeTrue())
			event := <-fakeRecorder.Events
			Expect(event).To(ContainSubstring("Creation succeeded"))
		})

		It("should update the profile status", func() {
			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
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
			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
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
			r := newFakeReconciler(profile, tunedOutdatedA, tunedOutdatedB, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
				var err error

				mc, err = machineconfig.New(profile, &infra.Status.CPUPartitioning, "")
				Expect(err).ToNot(HaveOccurred())

				mcpSelectorKey, mcpSelectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
				kc, err = kubeletconfig.New(profile, map[string]string{mcpSelectorKey: mcpSelectorValue})
				Expect(err).ToNot(HaveOccurred())

				tunedPerformance, err = tuned.NewNodePerformance(profile)
				Expect(err).ToNot(HaveOccurred())

				runtimeClass = runtimeclass.New(profile, machineconfig.HighPerformanceRuntime)
			})

			It("should not record new create event", func() {
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
					BalanceIsolated: pointer.BoolPtr(false),
				}

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
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
				r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
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

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, mcp, infra, clusterOperator, nodeConfig, profileMC)

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

				r := newFakeReconciler(profile, mc, kc, tunedPerformance, tuned, nodes, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
				r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
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
				r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
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

	Context("with the kubelet.experimental annotation set", func() {
		It("should create all resources on first reconcile loop", func() {
			prof := profile.DeepCopy()
			prof.Annotations = map[string]string{
				"kubeletconfig.experimental": `{"systemReserved": {"memory": "256Mi"}, "kubeReserved": {"memory": "256Mi"}}`,
			}
			r := newFakeReconciler(prof, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

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
			profile.DeletionTimestamp = &metav1.Time{
				Time: time.Now(),
			}
			profile.Finalizers = append(profile.Finalizers, finalizer)
		})

		It("should remove all components and remove the finalizer on first reconcile loop", func() {
			mc, err := machineconfig.New(profile, nil, "")
			Expect(err).ToNot(HaveOccurred())

			mcpSelectorKey, mcpSelectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := kubeletconfig.New(profile, map[string]string{mcpSelectorKey: mcpSelectorValue})
			Expect(err).ToNot(HaveOccurred())

			tunedPerformance, err := tuned.NewNodePerformance(profile)
			Expect(err).ToNot(HaveOccurred())

			runtimeClass := runtimeclass.New(profile, machineconfig.HighPerformanceRuntime)

			r := newFakeReconciler(profile, mc, kc, tunedPerformance, runtimeClass, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
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

	Context("with cgroups V1 enforcement", func() {
		BeforeEach(func() {
			infra = testutils.NewInfraResource(false)
			clusterOperator = testutils.NewClusterOperator()
			profileMCP = testutils.NewProfileMCP()
			profile = testutils.NewPerformanceProfile("test")
			request = reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: metav1.NamespaceNone,
					Name:      profile.Name,
				},
			}
		})

		It("should progress cleanly if the nodeconfig is already set to cgroup v1", func() {
			profileMC = testutils.NewProfileMachineConfig("test", kernelArgsv1)
			nodeConfig = testutils.NewNodeConfig(apiconfigv1.CgroupModeV1)

			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)
			Expect(reconcileTimes(r, request, 2)).To(Equal(reconcile.Result{}))

			updatedProfile := &performancev2.PerformanceProfile{}
			key := types.NamespacedName{
				Name:      profile.Name,
				Namespace: metav1.NamespaceNone,
			}
			Expect(r.Get(context.TODO(), key, updatedProfile)).To(Succeed())

			availableCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionAvailable)
			Expect(availableCondition).ToNot(BeNil())
			Expect(availableCondition.Status).To(Equal(corev1.ConditionTrue))
			message := strings.ToLower(availableCondition.Message)
			Expect(message).To(ContainSubstring("cgroup=v1")) // quite ugly. Should we add an explicit condition?
		})

		It("should set performance profile as degraded until the machineconfig is set to cgroup v1", func() {
			profileMC = testutils.NewProfileMachineConfig("test", []string{})
			nodeConfig = testutils.NewNodeConfig(apiconfigv1.CgroupModeEmpty)

			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

			reconcileTimes(r, request, 3)

			updatedProfile := &performancev2.PerformanceProfile{}
			key := types.NamespacedName{
				Name:      profile.Name,
				Namespace: metav1.NamespaceNone,
			}
			Expect(r.Get(context.TODO(), key, updatedProfile)).To(Succeed())

			degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
			Expect(degradedCondition.Status).To(Equal(corev1.ConditionTrue))
			Expect(degradedCondition.Reason).To(Equal(conditionReasonCgroupsV1NotEnabled))
		})

		It("should set continue with the performance profile reconciliation once the machineconfig is set to cgroup v1", func() {
			profileMC = testutils.NewProfileMachineConfig("test", []string{})
			nodeConfig = testutils.NewNodeConfig(apiconfigv1.CgroupModeEmpty)

			r := newFakeReconciler(profile, profileMCP, infra, clusterOperator, nodeConfig, profileMC)

			reconcileTimes(r, request, 3)

			updatedProfile := &performancev2.PerformanceProfile{}
			key := types.NamespacedName{
				Name:      profile.Name,
				Namespace: metav1.NamespaceNone,
			}
			Expect(r.Get(context.TODO(), key, updatedProfile)).To(Succeed())

			degradedCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionDegraded)
			Expect(degradedCondition.Status).To(Equal(corev1.ConditionTrue))
			Expect(degradedCondition.Reason).To(Equal(conditionReasonCgroupsV1NotEnabled))

			updatedProfileMC := profileMC.DeepCopy()
			updatedProfileMC.Spec.KernelArguments = kernelArgsv1

			Expect(r.Update(context.TODO(), updatedProfileMC)).To(Succeed())

			Expect(reconcileTimes(r, request, 2)).To(Equal(reconcile.Result{}))

			Expect(r.Get(context.TODO(), key, updatedProfile)).To(Succeed())

			availableCondition := conditionsv1.FindStatusCondition(updatedProfile.Status.Conditions, conditionsv1.ConditionAvailable)
			Expect(availableCondition).ToNot(BeNil())
			Expect(availableCondition.Status).To(Equal(corev1.ConditionTrue))
			message := strings.ToLower(availableCondition.Message)
			Expect(message).To(ContainSubstring("cgroup=v1")) // quite ugly. Should we add an explicit condition?
		})
	})

	Context("with infrastructure cpuPartitioning", func() {
		BeforeEach(func() {
			infra = testutils.NewInfraResource(true)
		})

		It("should contain cpu partitioning files in machine config", func() {
			mc, err := machineconfig.New(profile, &infra.Status.CPUPartitioning, "")
			Expect(err).ToNot(HaveOccurred())
			r := newFakeReconciler(profile, profileMCP, mc, infra, clusterOperator, nodeConfig, profileMC)

			Expect(reconcileTimes(r, request, 1)).To(Equal(reconcile.Result{}))

			By("Verifying MC update")
			key := types.NamespacedName{
				Name:      machineconfig.GetMachineConfigName(profile),
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
		r := newFakeReconciler(profile, mcp, nodeConfig)
		requests := r.mcpToPerformanceProfile(context.TODO(), mcp)
		Expect(requests).NotTo(BeEmpty())
		Expect(requests[0].Name).To(Equal(profile.Name))
	})

	Context("with ContainerRuntimeConfig enabling crun", func() {
		BeforeEach(func() {
			ctrcfg = testutils.NewContainerRuntimeConfig(mcov1.ContainerRuntimeDefaultRuntimeCrun, profile.Spec.MachineConfigPoolSelector)
		})

		It("should run high-performance runtimes class with crun as container-runtime", func() {
			mc, err := machineconfig.New(profile, &infra.Status.CPUPartitioning, "")
			Expect(err).ToNot(HaveOccurred())

			r := newFakeReconciler(profile, profileMCP, mc, infra, ctrcfg, clusterOperator, nodeConfig, profileMC)
			Expect(reconcileTimes(r, request, 2)).To(Equal(reconcile.Result{}))

			By("Verifying MC update")
			key := types.NamespacedName{
				Name:      machineconfig.GetMachineConfigName(profile),
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
						Path:  "/etc/crio/crio.conf.d/99-runtimes.conf",
						Group: &igntypes.NodeGroup{},
						User:  &igntypes.NodeUser{},
					},
					FileEmbedded1: igntypes.FileEmbedded1{
						Contents: igntypes.FileContents{
							Verification: igntypes.Verification{},
							Source:       "data:text/plain;charset=utf-8;base64,CltjcmlvLnJ1bnRpbWVdCmluZnJhX2N0cl9jcHVzZXQgPSAiMC0zIgoKCiMgV2Ugc2hvdWxkIGNvcHkgcGFzdGUgdGhlIGRlZmF1bHQgcnVudGltZSBiZWNhdXNlIHRoaXMgc25pcHBldCB3aWxsIG92ZXJyaWRlIHRoZSB3aG9sZSBydW50aW1lcyBzZWN0aW9uCltjcmlvLnJ1bnRpbWUucnVudGltZXMucnVuY10KcnVudGltZV9wYXRoID0gIiIKcnVudGltZV90eXBlID0gIm9jaSIKcnVudGltZV9yb290ID0gIi9ydW4vcnVuYyIKCiMgVGhlIENSSS1PIHdpbGwgY2hlY2sgdGhlIGFsbG93ZWRfYW5ub3RhdGlvbnMgdW5kZXIgdGhlIHJ1bnRpbWUgaGFuZGxlciBhbmQgYXBwbHkgaGlnaC1wZXJmb3JtYW5jZSBob29rcyB3aGVuIG9uZSBvZgojIGhpZ2gtcGVyZm9ybWFuY2UgYW5ub3RhdGlvbnMgcHJlc2VudHMgdW5kZXIgaXQuCiMgV2Ugc2hvdWxkIHByb3ZpZGUgdGhlIHJ1bnRpbWVfcGF0aCBiZWNhdXNlIHdlIG5lZWQgdG8gaW5mb3JtIHRoYXQgd2Ugd2FudCB0byByZS11c2UgcnVuYyBiaW5hcnkgYW5kIHdlCiMgZG8gbm90IGhhdmUgaGlnaC1wZXJmb3JtYW5jZSBiaW5hcnkgdW5kZXIgdGhlICRQQVRIIHRoYXQgd2lsbCBwb2ludCB0byBpdC4KW2NyaW8ucnVudGltZS5ydW50aW1lcy5oaWdoLXBlcmZvcm1hbmNlXQpydW50aW1lX3BhdGggPSAiL3Vzci9iaW4vY3J1biIKcnVudGltZV90eXBlID0gIm9jaSIKcnVudGltZV9yb290ID0gIi9ydW4vY3J1biIKYWxsb3dlZF9hbm5vdGF0aW9ucyA9IFsiY3B1LWxvYWQtYmFsYW5jaW5nLmNyaW8uaW8iLCAiY3B1LXF1b3RhLmNyaW8uaW8iLCAiaXJxLWxvYWQtYmFsYW5jaW5nLmNyaW8uaW8iLCAiY3B1LWMtc3RhdGVzLmNyaW8uaW8iLCAiY3B1LWZyZXEtZ292ZXJub3IuY3Jpby5pbyJdCg==",
						},
						Mode: &mode,
					},
				},
			}
			Expect(config.Storage.Files).To(ContainElements(containFiles))
		})
	})
})

func reconcileTimes(reconciler *PerformanceProfileReconciler, request reconcile.Request, times int) reconcile.Result {
	var result reconcile.Result
	var err error
	for i := 0; i < times; i++ {
		result, err = reconciler.Reconcile(context.TODO(), request)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}
	return result
}

func MCPInterceptor() interceptor.Funcs {
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
func newFakeReconciler(profile client.Object, initObjects ...runtime.Object) *PerformanceProfileReconciler {
	// we need to add the profile using the `WithStatusSubresource` function
	// because we're updating its status during the reconciliation loop
	initObjects = append(initObjects, profile)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(profile).WithRuntimeObjects(initObjects...).WithInterceptorFuncs(MCPInterceptor()).Build()
	fakeRecorder := record.NewFakeRecorder(10)
	return &PerformanceProfileReconciler{
		Client:   fakeClient,
		Scheme:   scheme.Scheme,
		Recorder: fakeRecorder,
	}
}
