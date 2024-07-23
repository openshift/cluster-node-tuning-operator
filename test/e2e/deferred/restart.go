package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	ntoutil "github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util/wait"
)

var _ = ginkgo.Describe("[deferred][restart][slow][disruptive][flaky] Profile deferred", ginkgo.Label("deferred", "restart", "slow", "disruptive", "flaky"), func() {
	ginkgo.Context("when restarting", func() {
		var (
			createdTuneds []string
			targetNode    *corev1.Node

			dirPath            string
			tunedPathSHMMNI    string
			tunedPathVMLatency string
			tunedObjSHMMNI     *tunedv1.Tuned
			tunedObjVMLatency  *tunedv1.Tuned
		)

		ginkgo.BeforeEach(func() {
			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			targetNode = &nodes[0]
			ginkgo.By(fmt.Sprintf("using node %q as reference", targetNode.Name))

			createdTuneds = []string{}

			dirPath, err = util.GetCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPathSHMMNI = filepath.Join(dirPath, tunedSHMMNI)
			tunedObjSHMMNI, err = util.LoadTuned(tunedPathSHMMNI)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			tunedPathVMLatency = filepath.Join(dirPath, tunedVMLatency)
			tunedObjVMLatency, err = util.LoadTuned(tunedPathVMLatency)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})

		ginkgo.AfterEach(func() {
			for _, createdTuned := range createdTuneds {
				ginkgo.By(fmt.Sprintf("cluster changes rollback: %q", createdTuned))
				util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", createdTuned)
			}
		})

		ginkgo.Context("[slow][pod]the tuned daemon", func() {
			ginkgo.It("should not be applied", func(ctx context.Context) {
				ginkgo.By(fmt.Sprintf("getting the tuned pod running on %q", targetNode.Name))
				targetTunedPod, err := util.GetTunedForNode(cs, targetNode)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.By(fmt.Sprintf("got the tuned pod running on %q: %s/%s %s", targetNode.Name, targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID))

				tunedImmediate := tunedObjVMLatency

				verifData := util.MustExtractVerificationOutputAndCommand(cs, targetNode, tunedImmediate)
				gomega.Expect(verifData.OutputCurrent).ToNot(gomega.Equal(verifData.OutputExpected), "current output %q already matches expected %q", verifData.OutputCurrent, verifData.OutputExpected)

				tunedMutated := setDeferred(tunedImmediate.DeepCopy())
				ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated.Annotations)))

				_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
				gomega.Expect(err).ToNot(gomega.HaveOccurred())

				createdTuneds = prepend(createdTuneds, tunedPathVMLatency) // we need the path, not the name
				ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

				gomega.Expect(tunedMutated.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedMutated.Name)
				gomega.Expect(tunedMutated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedMutated.Name)
				expectedProfile := *tunedMutated.Spec.Recommend[0].Profile
				ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

				gomega.Eventually(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
					if cond == nil {
						return fmt.Errorf("missing status applied condition")
					}
					err = checkAppliedConditionDeferred(cond, expectedProfile)
					if err != nil {
						util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					}
					return err
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

				oldTunedPodUID := targetTunedPod.UID
				ginkgo.By(fmt.Sprintf("killing the tuned pod running on %q", targetNode.Name))
				gomega.Expect(cs.Pods(targetTunedPod.GetNamespace()).Delete(ctx, targetTunedPod.Name, metav1.DeleteOptions{})).To(gomega.Succeed())
				// wait for the tuned pod to be found again

				gomega.Eventually(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
					if cond == nil {
						return fmt.Errorf("missing status applied condition")
					}
					err = checkAppliedConditionDeferred(cond, expectedProfile)
					if err != nil {
						util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					}
					return err
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

				ginkgo.By(fmt.Sprintf("getting again the tuned pod running on %q", targetNode.Name))
				gomega.Eventually(func() error {
					targetTunedPod, err = util.GetTunedForNode(cs, targetNode)
					if err != nil {
						return err
					}
					if targetTunedPod.UID == oldTunedPodUID {
						return fmt.Errorf("pod %s/%s not refreshed old UID %q current UID %q", targetTunedPod.Namespace, targetTunedPod.Name, oldTunedPodUID, targetTunedPod.UID)
					}
					if !wait.PodReady(*targetTunedPod) {
						return fmt.Errorf("pod %s/%s (%s) not ready", targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID)
					}
					return nil
				}).WithPolling(2 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
				ginkgo.By(fmt.Sprintf("got again the tuned pod running on %q: %s/%s %s", targetNode.Name, targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID))

				gomega.Consistently(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking conditions for profile %q: %#v", curProf.Name, curProf.Status.Conditions))
					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					for _, condition := range curProf.Status.Conditions {
						if condition.Type == tunedv1.TunedProfileApplied && condition.Status != corev1.ConditionFalse && condition.Reason != "Deferred" {
							return fmt.Errorf("Profile deferred=%v %s applied", ntoutil.HasDeferredUpdateAnnotation(curProf.Annotations), curProf.Name)
						}
						if condition.Type == tunedv1.TunedDegraded && condition.Status != corev1.ConditionTrue && condition.Reason != "TunedDeferredUpdate" {
							return fmt.Errorf("Profile deferred=%v %s not degraded", ntoutil.HasDeferredUpdateAnnotation(curProf.Annotations), curProf.Name)
						}
					}

					ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q did not change over pristine status using %v", curProf.Name, verifData.CommandArgs))
					out, err := util.ExecCmdInPod(targetTunedPod, verifData.CommandArgs...)
					if err != nil {
						return err
					}
					out = strings.TrimSpace(out)
					if out != verifData.OutputCurrent {
						return fmt.Errorf("got: %s; expected: %s", out, verifData.OutputCurrent)
					}
					return nil
				}).WithPolling(10 * time.Second).WithTimeout(2 * time.Minute).Should(gomega.Succeed())
			})
		})

		ginkgo.Context("[slow][disruptive][node] the worker node", func() {
			ginkgo.It("should be applied", func(ctx context.Context) {
				tunedImmediate := tunedObjVMLatency

				verifications := extractVerifications(tunedImmediate)
				gomega.Expect(len(verifications)).To(gomega.Equal(1), "unexpected verification count, check annotations")

				ginkgo.By(fmt.Sprintf("getting the tuned pod running on %q", targetNode.Name))
				targetTunedPod, err := util.GetTunedForNode(cs, targetNode)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.By(fmt.Sprintf("got the tuned pod running on %q: %s/%s %s", targetNode.Name, targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID))

				tunedMutated := setDeferred(tunedImmediate.DeepCopy())
				ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated.Annotations)))

				_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
				gomega.Expect(err).ToNot(gomega.HaveOccurred())

				createdTuneds = prepend(createdTuneds, tunedPathVMLatency) // we need the path, not the name

				gomega.Expect(tunedMutated.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedMutated.Name)
				gomega.Expect(tunedMutated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedMutated.Name)
				expectedProfile := *tunedMutated.Spec.Recommend[0].Profile
				ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

				gomega.Eventually(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
					if cond == nil {
						return fmt.Errorf("missing status applied condition")
					}
					err = checkAppliedConditionDeferred(cond, expectedProfile)
					if err != nil {
						util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					}
					return err
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

				ginkgo.By(fmt.Sprintf("getting the machine config daemon pod running on %q", targetNode.Name))
				targetMCDPod, err := util.GetMachineConfigDaemonForNode(cs, targetNode)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.By(fmt.Sprintf("got the machine config daemon pod running on %q: %s/%s %s", targetNode.Name, targetMCDPod.Namespace, targetMCDPod.Name, targetMCDPod.UID))

				ginkgo.By(fmt.Sprintf("restarting the worker node on which the tuned was running on %q", targetNode.Name))
				_, err = util.ExecCmdInPodNamespace(targetMCDPod.Namespace, targetMCDPod.Name, "chroot", "/rootfs", "systemctl", "reboot")
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				// Very generous timeout. On baremetal a reboot can take a long time
				wait.NodeBecomeReadyOrFail(cs, "post-reboot", targetNode.Name, 20*time.Minute, 5*time.Second)

				gomega.Eventually(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
					if cond == nil {
						return fmt.Errorf("missing status applied condition")
					}
					err = checkAppliedConditionOK(cond)
					if err != nil {
						util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					}
					return err
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

				ginkgo.By(fmt.Sprintf("getting again the tuned pod running on %q", targetNode.Name))
				targetTunedPod, err = util.GetTunedForNode(cs, targetNode)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.By(fmt.Sprintf("got again the tuned pod running on %q: %s/%s %s", targetNode.Name, targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID))

				gomega.Consistently(func() error {
					for _, verif := range verifications {
						ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q: %v -> %s", targetNode.Name, verif.command, verif.output))
						out, err := util.ExecCmdInPod(targetTunedPod, verif.command...)
						if err != nil {
							return err
						}
						out = strings.TrimSpace(out)
						if out != verif.output {
							return fmt.Errorf("got: %s; expected: %s", out, verif.output)
						}
					}
					return nil
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
			})

			ginkgo.It("should be reverted once applied and the node state should be restored", func(ctx context.Context) {
				tunedImmediate := tunedObjSHMMNI

				verifications := extractVerifications(tunedImmediate)
				gomega.Expect(len(verifications)).To(gomega.Equal(1), "unexpected verification count, check annotations")

				ginkgo.By(fmt.Sprintf("getting the tuned pod running on %q", targetNode.Name))
				targetTunedPod, err := util.GetTunedForNode(cs, targetNode)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.By(fmt.Sprintf("got the tuned pod running on %q: %s/%s %s", targetNode.Name, targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID))

				verifData := util.MustExtractVerificationOutputAndCommand(cs, targetNode, tunedImmediate)
				gomega.Expect(verifData.OutputCurrent).ToNot(gomega.Equal(verifData.OutputExpected), "verification output current %q matches expected %q", verifData.OutputCurrent, verifData.OutputExpected)

				tunedMutated := setDeferred(tunedImmediate.DeepCopy())
				ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated.Annotations)))

				_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
				gomega.Expect(err).ToNot(gomega.HaveOccurred())

				createdTuneds = prepend(createdTuneds, tunedPathSHMMNI) // we need the path, not the name

				gomega.Expect(tunedMutated.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedMutated.Name)
				gomega.Expect(tunedMutated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedMutated.Name)
				expectedProfile := *tunedMutated.Spec.Recommend[0].Profile
				ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

				gomega.Eventually(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
					if cond == nil {
						return fmt.Errorf("missing status applied condition")
					}
					err = checkAppliedConditionDeferred(cond, expectedProfile)
					if err != nil {
						util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					}
					return err
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

				ginkgo.By(fmt.Sprintf("getting the machine config daemon pod running on %q", targetNode.Name))
				targetMCDPod, err := util.GetMachineConfigDaemonForNode(cs, targetNode)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.By(fmt.Sprintf("got the machine config daemon pod running on %q: %s/%s %s", targetNode.Name, targetMCDPod.Namespace, targetMCDPod.Name, targetMCDPod.UID))

				ginkgo.By(fmt.Sprintf("restarting the worker node on which the tuned was running on %q", targetNode.Name))
				_, err = util.ExecCmdInPodNamespace(targetMCDPod.Namespace, targetMCDPod.Name, "chroot", "/rootfs", "systemctl", "reboot")
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				// Very generous timeout. On baremetal a reboot can take a long time
				wait.NodeBecomeReadyOrFail(cs, "post-reboot", targetNode.Name, 20*time.Minute, 5*time.Second)

				gomega.Eventually(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
					if cond == nil {
						return fmt.Errorf("missing status applied condition")
					}
					err = checkAppliedConditionOK(cond)
					if err != nil {
						util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					}
					return err
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

				ginkgo.By(fmt.Sprintf("getting again the tuned pod running on %q", targetNode.Name))
				targetTunedPod, err = util.GetTunedForNode(cs, targetNode)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.By(fmt.Sprintf("got again the tuned pod running on %q: %s/%s %s", targetNode.Name, targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID))

				gomega.Consistently(func() error {
					for _, verif := range verifications {
						ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q: %v -> %s", targetNode.Name, verif.command, verif.output))
						out, err := util.ExecCmdInPod(targetTunedPod, verif.command...)
						if err != nil {
							return err
						}
						out = strings.TrimSpace(out)
						if out != verif.output {
							return fmt.Errorf("got: %s; expected: %s", out, verif.output)
						}
					}
					return nil
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

				ginkgo.By(fmt.Sprintf("cluster changes rollback: %q", tunedPathSHMMNI))
				_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", tunedPathSHMMNI)
				gomega.Expect(err).ToNot(gomega.HaveOccurred())

				_, createdTuneds, _ = popleft(createdTuneds)
				gomega.Eventually(func() error {
					curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

					if len(curProf.Status.Conditions) == 0 {
						return fmt.Errorf("missing status conditions")
					}
					cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
					if cond == nil {
						return fmt.Errorf("missing status applied condition")
					}
					err = checkAppliedConditionOK(cond)
					if err != nil {
						util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					}

					ginkgo.By(fmt.Sprintf("checking real node conditions are restored pristine: %v -> %s", verifData.CommandArgs, verifData.OutputCurrent))
					out, err := util.ExecCmdInPod(targetTunedPod, verifData.CommandArgs...)
					if err != nil {
						return err
					}
					out = strings.TrimSpace(out)
					if out != verifData.OutputCurrent {
						return fmt.Errorf("got: %s; expected: %s", out, verifData.OutputCurrent)
					}
					return nil
				}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
			})
		})
	})
})
