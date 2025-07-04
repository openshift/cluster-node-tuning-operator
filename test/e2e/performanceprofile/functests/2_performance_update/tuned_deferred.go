package __performance_update

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gopkg.in/ini.v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	tunedutil "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"
)

type TunedProfileConfig struct {
	DeferMode         string
	ProfileChangeType string // "first-time" or "in-place"
	ExpectedBehavior  string // "deferred" or "immediate"
	KernelSHMMNI      string // 4096 or 8192
	ServiceStalld     string // "start, enabled"
	CmdlineCPUPart    string // for bootloader
	CmdlineIsolation  string // for bootloader
	CmdlineIdlePoll   string // for bootloader
}

var _ = Describe("Tuned Deferred tests of performance profile", Ordered, Label(string(label.TunedDeferred)), func() {
	var (
		workerCNFNodes []corev1.Node
		err            error
		poolName       string
		tuned          *tunedv1.Tuned
		initialPolicy  *string
	)

	name := "performance-patch"

	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		workerCNFNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerCNFNodes, err = nodes.MatchingOptionalSelector(workerCNFNodes)
		Expect(err).ToNot(HaveOccurred(), "error looking for the optional selector: %v", err)
		Expect(workerCNFNodes).ToNot(BeEmpty())

		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		initialPolicy = profile.Spec.NUMA.TopologyPolicy

		poolName, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("using performanceMCP: %q", poolName)
	})

	Context("Tuned Deferred status", func() {
		DescribeTable("Validate Tuned DeferMode behavior",
			func(tc TunedProfileConfig) {
				var data string
				if tc.ProfileChangeType == "first-time" {
					tuned = getTunedProfile(name)

					// If profile is present
					if tuned != nil {

						// Delete the profile
						testlog.Infof("Deleting Tuned Profile Patch: %s", name)
						Expect(testclient.ControlPlaneClient.Delete(context.TODO(), tuned)).To(Succeed())
						Eventually(func() bool {
							profile, _ := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
							return profile.Spec.Config.TunedProfile == "openshift-node-performance-performance"
						}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Tuned profile deletion did not complete in time")
					}
					if tc.KernelSHMMNI != "" || tc.ServiceStalld != "" || tc.CmdlineCPUPart != "" || tc.CmdlineIsolation != "" || tc.CmdlineIdlePoll != "" {
						data = createTunedData(tc)
					}
					tuned, err = createTunedObject(tc.DeferMode, data, name)
					Expect(err).NotTo(HaveOccurred())

				} else {
					tuned := getTunedProfile(name)
					tunedData := []byte(*tuned.Spec.Profile[0].Data)

					cfg, err := ini.Load(tunedData)
					Expect(err).ToNot(HaveOccurred())

					if tc.KernelSHMMNI != "" {
						sysctlSection, err := cfg.GetSection("sysctl")
						Expect(err).ToNot(HaveOccurred())

						sysctlSection.Key("kernel.shmmni").SetValue(tc.KernelSHMMNI)
						Expect(sysctlSection.Key("kernel.shmmni").String()).To(Equal(tc.KernelSHMMNI))
					} else if tc.ServiceStalld != "" {
						serviceSection, err := cfg.GetSection("service")
						Expect(err).ToNot(HaveOccurred())

						serviceSection.Key("service.stalld").SetValue(tc.ServiceStalld)
						Expect(serviceSection.Key("service.stalld").String()).To(Equal(tc.ServiceStalld))
					} else if tc.CmdlineCPUPart != "" {
						bootloaderSection, err := cfg.GetSection("bootloader")
						Expect(err).ToNot(HaveOccurred())
						bootloaderSection.Key("cmdline_cpu_part").SetValue(tc.CmdlineCPUPart)
						Expect(bootloaderSection.Key("cmdline_cpu_part").String()).To(Equal(tc.CmdlineCPUPart))
					} else if tc.CmdlineIsolation != "" {
						bootloaderSection, err := cfg.GetSection("bootloader")
						Expect(err).ToNot(HaveOccurred())
						bootloaderSection.Key("cmdline_isolation").SetValue(tc.CmdlineIsolation)
						Expect(bootloaderSection.Key("cmdline_isolation").String()).To(Equal(tc.CmdlineIsolation))
					} else if tc.CmdlineIdlePoll != "" {
						bootloaderSection, err := cfg.GetSection("bootloader")
						Expect(err).ToNot(HaveOccurred())
						bootloaderSection.Key("cmdline_idle_poll").SetValue(tc.CmdlineIdlePoll)
						Expect(bootloaderSection.Key("cmdline_idle_poll").String()).To(Equal(tc.CmdlineIdlePoll))
					}

					var updatedTunedData bytes.Buffer
					_, err = cfg.WriteTo(&updatedTunedData)
					Expect(err).ToNot(HaveOccurred())

					updatedTunedDataBytes := updatedTunedData.Bytes()
					tuned.Spec.Profile[0].Data = stringPtr(string(updatedTunedDataBytes))

					testlog.Infof("Updating Tuned Profile Patch: %s", name)
					Expect(testclient.ControlPlaneClient.Update(context.TODO(), tuned)).To(Succeed())
					// For in-place updates, we need to check if the substring is present in the updated profile data
					// The actual value check happens in verifyImmediateApplication or verifyDeferredApplication
					if tc.KernelSHMMNI != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*time.Second).Should(ContainSubstring(tc.KernelSHMMNI), "The updated tuned profile section did not contain the expected kernel.shmmni value")
					} else if tc.ServiceStalld != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*time.Second).Should(ContainSubstring(tc.ServiceStalld), "The updated tuned profile section did not contain the expected service.stalld value")
					} else if tc.CmdlineCPUPart != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*time.Second).Should(ContainSubstring(tc.CmdlineCPUPart), "The updated tuned profile section did not contain the expected cmdline_cpu_part value")
					} else if tc.CmdlineIsolation != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*time.Second).Should(ContainSubstring(tc.CmdlineIsolation), "The updated tuned profile section did not contain the expected cmdline_isolation value")
					} else if tc.CmdlineIdlePoll != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*time.Second).Should(ContainSubstring(tc.CmdlineIdlePoll), "The updated tuned profile section did not contain the expected cmdline_idle_poll value")
					}
				}

				switch tc.ExpectedBehavior {
				case "immediate":
					verifyImmediateApplication(tuned, workerCNFNodes, tc)
				case "deferred":
					Eventually(func() bool {
						result, err := verifyDeferredApplication(tuned, workerCNFNodes, name)
						Expect(err).To(BeNil())
						return result
					}, 1*time.Minute, 5*time.Second).Should(BeTrue(), "Timed out checking deferred condition message")
					rebootNode(poolName)
					verifyImmediateApplication(tuned, workerCNFNodes, tc)
				}
			},
			Entry("[test_id:78115] Verify deferred Always with first-time profile change", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "deferred",
				KernelSHMMNI:      "8192",
			}),
			Entry("[test_id:78116] Verify deferred Always with in-place profile update", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				KernelSHMMNI:      "4096",
			}),
			Entry("[test_id:78117] Verify deferred Update with first-time profile change", TunedProfileConfig{
				DeferMode:         "update",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "immediate",
				KernelSHMMNI:      "8192",
			}),
			Entry("[test_id:78118] Verify deferred Update with in-place profile update", TunedProfileConfig{
				DeferMode:         "update",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				KernelSHMMNI:      "4096",
			}),
			Entry("[test_id:78119] Verify deferred with No annotation with first-time profile change", TunedProfileConfig{
				DeferMode:         "",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "immediate",
				KernelSHMMNI:      "8192",
			}),
			Entry("[test_id:78120] Verify deferred Never mode with in-place profile update", TunedProfileConfig{
				DeferMode:         "never",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "immediate",
				KernelSHMMNI:      "4096",
			}),
			Entry("Verify deferred Always with first-time profile change - service plugin", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "deferred",
				ServiceStalld:     "start",
			}),
			Entry("Verify deferred Always with in-place profile update - service plugin", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				ServiceStalld:     "start, enabled",
			}),
		)
	})

	Context("Tuned Deferred status for Bootloader parameters", func() {
		DescribeTable("Validate Tuned DeferMode behavior for Bootloader parameters",
			func(tc TunedProfileConfig) {
				var data string
				if tc.ProfileChangeType == "first-time" {
					tuned = getTunedProfile(name)

					// If profile is present
					if tuned != nil {
						testlog.Infof("Deleting Tuned Profile Patch: %s", name)
						Expect(testclient.ControlPlaneClient.Delete(context.TODO(), tuned)).To(Succeed())
						Eventually(func() bool {
							profile, _ := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
							return profile.Spec.Config.TunedProfile == "openshift-node-performance-performance"
						}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Tuned profile deletion did not complete in time")
					}
					data = createTunedData(tc)
					tuned, err = createTunedObject(tc.DeferMode, data, name)
					Expect(err).NotTo(HaveOccurred())

				} else { // in-place update for bootloader parameters
					tuned := getTunedProfile(name)
					tunedData := []byte(*tuned.Spec.Profile[0].Data)

					cfg, err := ini.Load(tunedData)
					Expect(err).ToNot(HaveOccurred())

					bootloaderSection, err := cfg.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())

					if tc.CmdlineCPUPart != "" {
						bootloaderSection.Key("cmdline_cpu_part").SetValue(tc.CmdlineCPUPart)
						Expect(bootloaderSection.Key("cmdline_cpu_part").String()).To(Equal(tc.CmdlineCPUPart))
					} else if tc.CmdlineIsolation != "" {
						bootloaderSection.Key("cmdline_isolation").SetValue(tc.CmdlineIsolation)
						Expect(bootloaderSection.Key("cmdline_isolation").String()).To(Equal(tc.CmdlineIsolation))
					} else if tc.CmdlineIdlePoll != "" {
						bootloaderSection.Key("cmdline_idle_poll").SetValue(tc.CmdlineIdlePoll)
						Expect(bootloaderSection.Key("cmdline_idle_poll").String()).To(Equal(tc.CmdlineIdlePoll))
					}

					var updatedTunedData bytes.Buffer
					_, err = cfg.WriteTo(&updatedTunedData)
					Expect(err).ToNot(HaveOccurred())

					updatedTunedDataBytes := updatedTunedData.Bytes()
					tuned.Spec.Profile[0].Data = stringPtr(string(updatedTunedDataBytes))

					testlog.Infof("Updating Tuned Profile Patch: %s", name)
					Expect(testclient.ControlPlaneClient.Update(context.TODO(), tuned)).To(Succeed())

					// Verify the profile data has been updated
					if tc.CmdlineCPUPart != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*second).Should(ContainSubstring(tc.CmdlineCPUPart), "The updated tuned profile section did not contain the expected cmdline_cpu_part value")
					} else if tc.CmdlineIsolation != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*second).Should(ContainSubstring(tc.CmdlineIsolation), "The updated tuned profile section did not contain the expected cmdline_isolation value")
					} else if tc.CmdlineIdlePoll != "" {
						Eventually(func() string {
							patch := getTunedProfile(name)
							return *patch.Spec.Profile[0].Data
						}, 1*time.Minute, 5*second).Should(ContainSubstring(tc.CmdlineIdlePoll), "The updated tuned profile section did not contain the expected cmdline_idle_poll value")
					}
				}

				// Bootloader parameters always require deferred application
				Expect(tc.ExpectedBehavior).To(Equal("deferred"), "Bootloader parameters should always be deferred")

				Eventually(func() bool {
					result, err := verifyDeferredApplication(tuned, workerCNFNodes, name)
					Expect(err).To(BeNil())
					return result
				}, 1*time.Minute, 5*time.Second).Should(BeTrue(), "Timed out checking deferred condition message for bootloader parameter")
				rebootNode(poolName)
				verifyImmediateApplication(tuned, workerCNFNodes, tc)
			},
			Entry("Verify deferred Always with first-time profile change - cmdline_cpu_part", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "deferred",
				CmdlineCPUPart:    "+nohz=on rcu_nocbs=1,2",
			}),
			Entry("Verify deferred Always with in-place profile update - cmdline_cpu_part", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				CmdlineCPUPart:    "+nohz=on rcu_nocbs=3,4",
			}),
			Entry("Verify deferred Always with first-time profile change - cmdline_isolation", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "deferred",
				CmdlineIsolation:  "+isolcpus=managed_irq,5",
			}),
			Entry("Verify deferred Always with in-place profile update - cmdline_isolation", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				CmdlineIsolation:  "+isolcpus=domain,managed_irq,6",
			}),
			Entry("Verify deferred Always with first-time profile change - cmdline_idle_poll", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "deferred",
				CmdlineIdlePoll:   "+idle=poll",
			}),
			Entry("Verify deferred Always with in-place profile update - cmdline_idle_poll", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				CmdlineIdlePoll:   "+processor.max_cstate=0",
			}),
		)
	})

	AfterAll(func() {
		tuned = getTunedProfile(name)
		if tuned != nil {
			Expect(testclient.ControlPlaneClient.Delete(context.TODO(), tuned)).To(Succeed())
		}
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Unable to fetch latest performance profile err: %v", err))
		if *profile.Spec.NUMA.TopologyPolicy != *initialPolicy {
			testlog.Infof("current policy: ", *profile.Spec.NUMA.TopologyPolicy)
			testlog.Infof("initial policy: ", *initialPolicy)
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: initialPolicy,
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		}
	})

})

func stringPtr(s string) *string {
	return &s
}

func getTunedProfile(tunedName string) *tunedv1.Tuned {
	GinkgoHelper()

	tunedList := &tunedv1.TunedList{}
	var matchedTuned *tunedv1.Tuned

	Eventually(func(g Gomega) {
		g.Expect(testclient.DataPlaneClient.List(context.TODO(), tunedList, &client.ListOptions{
			Namespace: components.NamespaceNodeTuningOperator,
		})).To(Succeed())

		g.Expect(tunedList.Items).ToNot(BeEmpty())

		for _, tuned := range tunedList.Items {
			if tuned.Name == tunedName {
				matchedTuned = &tuned
				break
			}
		}
	}).WithTimeout(time.Minute * 3).WithPolling(time.Second * 10).Should(Succeed())

	if matchedTuned == nil {
		return nil
	}
	return matchedTuned
}

func createTunedObject(deferMode string, data string, name string) (*tunedv1.Tuned, error) {
	GinkgoHelper()

	tunedName := name
	ns := components.NamespaceNodeTuningOperator
	priority := uint64(19)

	// Create a Tuned object
	tuned := &tunedv1.Tuned{
		TypeMeta: metav1.TypeMeta{
			APIVersion: tunedv1.SchemeGroupVersion.String(),
			Kind:       "Tuned",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tunedName,
			Namespace: ns,
			Annotations: map[string]string{
				"tuned.openshift.io/deferred": deferMode,
			},
		},
		Spec: tunedv1.TunedSpec{
			Profile: []tunedv1.TunedProfile{
				{
					Name: &tunedName,
					Data: &data,
				},
			},
			Recommend: []tunedv1.TunedRecommend{
				{
					MachineConfigLabels: map[string]string{"machineconfiguration.openshift.io/role": testutils.RoleWorkerCNF},
					Priority:            &priority,
					Profile:             &tunedName,
				},
			},
		},
	}

	testlog.Infof("Creating Tuned Profile Patch: %s", name)
	Expect(testclient.ControlPlaneClient.Create(context.TODO(), tuned)).To(Succeed())
	return tuned, nil
}

func createTunedData(tc TunedProfileConfig) string {
	var data string
	switch {
	case tc.KernelSHMMNI != "":
		data = fmt.Sprintf(`
		[main]
		summary=Configuration changes profile inherited from performance created tuned
		include=openshift-node-performance-performance
		[sysctl]
		kernel.shmmni=%s
		`, tc.KernelSHMMNI)

	case tc.ServiceStalld != "":
		data = fmt.Sprintf(`
		[main]
		summary=Configuration changes profile inherited from performance created tuned
		include=openshift-node-performance-performance
		[service]
		service.stalld=%s
		`, tc.ServiceStalld)
	case tc.CmdlineCPUPart != "" || tc.CmdlineIsolation != "" || tc.CmdlineIdlePoll != "":
		bootloaderConfig := ""
		if tc.CmdlineCPUPart != "" {
			bootloaderConfig += fmt.Sprintf("cmdline_cpu_part=%s\n", tc.CmdlineCPUPart)
		}
		if tc.CmdlineIsolation != "" {
			bootloaderConfig += fmt.Sprintf("cmdline_isolation=%s\n", tc.CmdlineIsolation)
		}
		if tc.CmdlineIdlePoll != "" {
			bootloaderConfig += fmt.Sprintf("cmdline_idle_poll=%s\n", tc.CmdlineIdlePoll)
		}

		data = fmt.Sprintf(`
		[main]
		summary=Configuration changes profile inherited from performance created tuned
		include=openshift-node-performance-performance
		[bootloader]
		%s`, bootloaderConfig)
	}
	return data
}

func verifyOnWorkers(ctx context.Context, workerNodes []corev1.Node, paramMap map[string]string) {
	GinkgoHelper()

	var err error
	var out []byte
	isSNO := false
	for _, node := range workerNodes {
		for param, expected := range paramMap {
			Eventually(func() bool {
				var tunedCmd []string
				switch param {
				case "kernel.shmmni":
					By(fmt.Sprintf("Checking sysctl value of %s", param))
					tunedCmd = []string{"/bin/sh", "-c", fmt.Sprintf("chroot /host sysctl -n %s", param)}
				case "service.stalld":
					expected = "active" // For service.stalld, we check if the service is active
					By("Checking if 'stalld' service is active")
					tunedCmd = []string{"/bin/sh", "-c", "chroot /host systemctl is-active stalld"}
				case "cmdline_cpu_part", "cmdline_isolation", "cmdline_idle_poll":
					By(fmt.Sprintf("Checking kernel command line for %s", param))
					tunedCmd = []string{"/bin/sh", "-c", "chroot /host cat /proc/cmdline"}
				}

				tunedPod := nodes.TunedForNode(&node, isSNO)
				out, err = pods.WaitForPodOutput(ctx, testclient.K8sClient, tunedPod, tunedCmd)
				Expect(err).ToNot(HaveOccurred())
				res := strings.TrimSpace(string(out))

				// For bootloader parameters, we check if the expected string is contained within the full cmdline
				if strings.HasPrefix(param, "cmdline_") {
					if !strings.Contains(res, expected) {
						testlog.Errorf("Kernel command line '%s' does not contain expected value '%s' for parameter %s", res, expected, param)
						return false
					}
					return true
				} else {
					if res != expected {
						testlog.Errorf("parameter %s returned '%s', expected '%s'", param, res, expected)
					}
					return res == expected
				}
			}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
				fmt.Sprintf("Timed out verifying %s on worker node %s", param, node.Name))
		}
	}
}

func verifyImmediateApplication(tuned *tunedv1.Tuned, workerCNFNodes []corev1.Node, tc TunedProfileConfig) {
	// Use Eventually to wait until the performance profile status is updated
	Eventually(func() bool {
		profile, _ := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
		return profile.Spec.Config.TunedProfile == tuned.Name
	}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Timed out waiting for profile.Spec.Config.TunedProfile to match tuned.Name")

	paramMap := make(map[string]string)
	switch {
	case tc.KernelSHMMNI != "":
		paramMap["kernel.shmmni"] = tc.KernelSHMMNI
	case tc.ServiceStalld != "":
		paramMap["service.stalld"] = tc.ServiceStalld
	case tc.CmdlineCPUPart != "":
		paramMap["cmdline_cpu_part"] = tc.CmdlineCPUPart
	case tc.CmdlineIsolation != "":
		paramMap["cmdline_isolation"] = tc.CmdlineIsolation
	case tc.CmdlineIdlePoll != "":
		paramMap["cmdline_idle_poll"] = tc.CmdlineIdlePoll
	}
	verifyOnWorkers(context.TODO(), workerCNFNodes, paramMap)
}

func verifyDeferredApplication(tuned *tunedv1.Tuned, workerCNFNodes []corev1.Node, name string) (bool, error) {
	// Use Eventually to wait until the performance profile status is updated
	Eventually(func() bool {
		profile, _ := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
		return profile.Spec.Config.TunedProfile == tuned.Name
	}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Timed out waiting for profile.Spec.Config.TunedProfile to match tuned.Name")

	expectedMessage := fmt.Sprintf("The TuneD daemon profile is waiting for the next node restart: %s", name)
	profile, err := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
	if err != nil {
		return false, fmt.Errorf("failed to get profile: %w", err)
	}

	for _, condition := range profile.Status.Conditions {
		if condition.Message == expectedMessage {
			return true, nil
		}
	}

	return false, nil
}

func rebootNode(poolName string) {
	fmt.Println("\n\n\n\n Rebooting NODE")
	profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	if err != nil {
		testlog.Errorf("Unable to fetch latest performance profile err: %v", err)
	}
	testlog.Info("Rebooting the node")
	// reboot the node, for that we change the numa policy to best-effort
	// Note: this is used only to trigger reboot
	policy := "best-effort"
	// Need to make some changes to pp , causing system reboot
	currentPolicy := profile.Spec.NUMA.TopologyPolicy
	if *currentPolicy == "best-effort" {
		policy = "single-numa-node"
	}
	profile.Spec.NUMA = &performancev2.NUMA{
		TopologyPolicy: &policy,
	}

	By("Updating the performance profile")
	profiles.UpdateWithRetry(profile)

	By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
	profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

	By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
	profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
}
