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
)

var _ = ginkgo.Describe("Profile deferred", ginkgo.Label("deferred", "profile-status"), func() {
	ginkgo.Context("when applied", func() {
		var (
			createdTuneds     []string
			referenceNode     *corev1.Node // control plane
			targetNode        *corev1.Node
			referenceTunedPod *corev1.Pod // control plane
			referenceProfile  string

			dirPath            string
			tunedPathVMLatency string
			tunedObjVMLatency  *tunedv1.Tuned
		)

		ginkgo.BeforeEach(func() {
			ginkgo.By("getting a list of worker nodes")
			workerNodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(workerNodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			targetNode = &workerNodes[0]
			ginkgo.By(fmt.Sprintf("using node %q as target for workers", targetNode.Name))

			ginkgo.By("getting a list of control-plane nodes")
			controlPlaneNodes, err := util.GetNodesByRole(cs, "control-plane")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(controlPlaneNodes)).NotTo(gomega.BeZero(), "number of control plane nodes is 0")

			referenceNode = &controlPlaneNodes[0]
			ginkgo.By(fmt.Sprintf("using node %q as reference control plane", referenceNode.Name))

			referenceTunedPod, err = util.GetTunedForNode(cs, referenceNode)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(referenceTunedPod.Status.Phase).To(gomega.Equal(corev1.PodRunning))

			referenceProfile, err = getRecommendedProfile(referenceTunedPod)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("using profile %q as reference control plane", referenceProfile))

			createdTuneds = []string{}

			dirPath, err = util.GetCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPathVMLatency = filepath.Join(dirPath, tunedVMLatency)
			tunedObjVMLatency, err = util.LoadTuned(tunedPathVMLatency)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})

		ginkgo.AfterEach(func() {
			// Ignore failures to cleanup resources which are already deleted or not yet created.
			for _, createdTuned := range createdTuneds {
				ginkgo.By(fmt.Sprintf("cluster changes rollback: %q", createdTuned))
				_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", createdTuned)
			}
			_, _, _ = util.ExecAndLogCommand("oc", "label", "node", targetNode.Name, tunedMatchLabelLater+"-")

			checkWorkerNodeIsDefaultState(context.Background(), targetNode)
		})

		ginkgo.It("should not trigger any actual change", func(ctx context.Context) {
			tunedPath := filepath.Join(dirPath, tunedSHMMNI)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPath, dirPath))

			tuned, err := util.LoadTuned(tunedPath)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			verifData := util.MustExtractVerificationOutputAndCommand(cs, targetNode, tuned)
			gomega.Expect(verifData.OutputCurrent).ToNot(gomega.Equal(verifData.OutputExpected), "current output %q already matches expected %q", verifData.OutputCurrent, verifData.OutputExpected)

			tunedMutated := setDeferred(tuned.DeepCopy(), ntoutil.DeferAlways)
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.GetDeferredUpdateAnnotation(tunedMutated.Annotations)))

			tunedCreated, err := cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPath) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedCreated.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedCreated.Name)
			gomega.Expect(tunedCreated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedCreated.Name)
			expectedProfile := *tunedCreated.Spec.Recommend[0].Profile
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
				recommended, err := getRecommendedProfile(verifData.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

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
						return fmt.Errorf("Profile deferred=%v %s applied", ntoutil.GetDeferredUpdateAnnotation(curProf.Annotations), curProf.Name)
					}
					if condition.Type == tunedv1.TunedDegraded && condition.Status != corev1.ConditionTrue && condition.Reason != "TunedDeferredUpdate" {
						return fmt.Errorf("Profile deferred=%v %s not degraded", ntoutil.GetDeferredUpdateAnnotation(curProf.Annotations), curProf.Name)
					}
				}
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q are not changed from pristine state", curProf.Name))
				out, err := util.ExecCmdInPod(verifData.TargetTunedPod, verifData.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifData.OutputCurrent {
					return fmt.Errorf("got: %s; expected: %s", out, verifData.OutputCurrent)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(2 * time.Minute).Should(gomega.Succeed())

			// on non-targeted nodes, like control plane, nothing should have changed
			checkAppliedConditionStaysOKForNode(ctx, referenceNode.Name, referenceProfile)
		})

		ginkgo.It("should revert the profile status on removal", func(ctx context.Context) {
			dirPath, err := util.GetCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPath := filepath.Join(dirPath, tunedSHMMNI)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPath, dirPath))

			tuned, err := util.LoadTuned(tunedPath)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			// gather the output now before the profile is applied so we can check nothing changed
			verifData := util.MustExtractVerificationOutputAndCommand(cs, targetNode, tuned)
			gomega.Expect(verifData.OutputCurrent).ToNot(gomega.Equal(verifData.OutputExpected), "current output %q already matches expected %q", verifData.OutputCurrent, verifData.OutputExpected)

			tunedMutated := setDeferred(tuned.DeepCopy(), ntoutil.DeferAlways)
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.GetDeferredUpdateAnnotation(tunedMutated.Annotations)))

			tunedCreated, err := cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPath) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedCreated.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedCreated.Name)
			gomega.Expect(tunedCreated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedCreated.Name)
			expectedProfile := *tunedCreated.Spec.Recommend[0].Profile
			ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

			gomega.Expect(verifData.TargetTunedPod.Status.Phase).To(gomega.Equal(corev1.PodRunning), "TargetTunedPod %s/%s uid %s phase %s", verifData.TargetTunedPod.Namespace, verifData.TargetTunedPod.Name, verifData.TargetTunedPod.UID, verifData.TargetTunedPod.Status.Phase)

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
				recommended, err := getRecommendedProfile(verifData.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			// on non-targeted nodes, like control plane, nothing should have changed
			checkAppliedConditionStaysOKForNode(ctx, referenceNode.Name, referenceProfile)

			ginkgo.By(fmt.Sprintf("cluster changes rollback: %q", tunedPath))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", tunedPath)
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

				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q", curProf.Name))
				out, err := util.ExecCmdInPod(verifData.TargetTunedPod, verifData.CommandArgs...)
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

		ginkgo.It("should be overridden by another deferred update", func(ctx context.Context) {
			dirPath, err := util.GetCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPathSHMMNI := filepath.Join(dirPath, tunedSHMMNI)
			tunedDeferred, err := util.LoadTuned(tunedPathSHMMNI)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedMutated := setDeferred(tunedDeferred.DeepCopy(), ntoutil.DeferAlways)
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.GetDeferredUpdateAnnotation(tunedMutated.Annotations)))

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathSHMMNI) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedMutated.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedMutated.Name)
			gomega.Expect(tunedMutated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedMutated.Name)
			expectedProfile := *tunedMutated.Spec.Recommend[0].Profile
			ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

			targetTunedPod, err := util.GetTunedForNode(cs, targetNode)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(targetTunedPod.Status.Phase).To(gomega.Equal(corev1.PodRunning), targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID, targetTunedPod.Status.Phase)

			// on non-targeted nodes, like control plane, nothing should have changed
			checkAppliedConditionStaysOKForNode(ctx, referenceNode.Name, referenceProfile)

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
				recommended, err := getRecommendedProfile(targetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			tunedDeferred2 := tunedObjVMLatency
			tunedMutated2 := setDeferred(tunedDeferred2.DeepCopy(), ntoutil.DeferAlways)
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated2.Name, ntoutil.GetDeferredUpdateAnnotation(tunedMutated2.Annotations)))

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated2, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathVMLatency) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedMutated2.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedMutated2.Name)
			gomega.Expect(tunedMutated2.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedMutated2.Name)
			expectedProfile = *tunedMutated2.Spec.Recommend[0].Profile
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

				recommended, err := getRecommendedProfile(targetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(5 * time.Minute).Should(gomega.Succeed())

			ginkgo.Fail("XXX TEST TEST")
		})

		ginkgo.It("should be overridden by a immediate update by edit", func(ctx context.Context) {
			tunedImmediate := tunedObjVMLatency
			tunedMutated := setDeferred(tunedImmediate.DeepCopy(), ntoutil.DeferAlways)
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.GetDeferredUpdateAnnotation(tunedMutated.Annotations)))

			_, err := cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathVMLatency) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedMutated.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedMutated.Name)
			gomega.Expect(tunedMutated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedMutated.Name)
			expectedProfile := *tunedMutated.Spec.Recommend[0].Profile
			ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

			targetTunedPod, err := util.GetTunedForNode(cs, targetNode)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(targetTunedPod.Status.Phase).To(gomega.Equal(corev1.PodRunning), targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID, targetTunedPod.Status.Phase)

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
				recommended, err := getRecommendedProfile(targetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			gomega.Eventually(func() error {
				curTuned, err := cs.Tuneds(ntoconfig.WatchNamespace()).Get(ctx, tunedImmediate.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				curTuned = curTuned.DeepCopy()

				ginkgo.By(fmt.Sprintf("removing the deferred annotation from Tuned %q", tunedImmediate.Name))
				curTuned.Annotations = ntoutil.DeleteDeferredUpdateAnnotation(curTuned.Annotations)

				_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Update(ctx, curTuned, metav1.UpdateOptions{})
				return err
			}).WithPolling(1*time.Second).WithTimeout(1*time.Minute).Should(gomega.Succeed(), "cannot remove the deferred annotation")

			verifications := extractVerifications(tunedImmediate)
			gomega.Expect(len(verifications)).To(gomega.Equal(1), "unexpected verification count, check annotations")

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

			gomega.Consistently(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q", targetNode.Name))
				for _, verif := range verifications {
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

		ginkgo.It("should apply defer-on-update immediately after node is labelled", ginkgo.Label("node-label"), func(ctx context.Context) {
			nodeLabel := tunedMatchLabelLater
			_, ok := targetNode.Labels[nodeLabel]
			ginkgo.By(fmt.Sprintf("checking the target node labels: on %q: %q present = %v", targetNode.Name, nodeLabel, ok))
			util.Logf("labels on %q: %v", targetNode.Name, targetNode.Labels)

			tunedPath := filepath.Join(dirPath, tunedSHMMNI)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPath, dirPath))

			tuned, err := util.LoadTuned(tunedPath)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			verifData := util.MustExtractVerificationOutputAndCommand(cs, targetNode, tuned)
			gomega.Expect(verifData.OutputCurrent).ToNot(gomega.Equal(verifData.OutputExpected), "current output %q already matches expected %q", verifData.OutputCurrent, verifData.OutputExpected)

			tunedMutated := setDeferred(tuned.DeepCopy(), ntoutil.DeferUpdate)

			gomega.Expect(tuned.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tuned.Name)
			gomega.Expect(tuned.Spec.Recommend[0].Match).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendation matchers", tuned.Name)

			// Change the recommendation to use non-role label to test a scenario where the Tuned is created first (and processed by NTO)
			// and only later a node is changed to consume it. This results in a flow where no change is detected on the Tuned level (it is already)
			// on the disk, but an update must be triggered.
			tunedMutated.Spec.Recommend[0].Match[0].Label = &nodeLabel

			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.GetDeferredUpdateAnnotation(tunedMutated.Annotations)))
			tunedCreated, err := cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPath) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedCreated.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedCreated.Name)
			expectedProfile := *tuned.Spec.Recommend[0].Profile

			checkCurrentProfileIsDefaultForWorker(ctx, targetNode.Name, verifData.TargetTunedPod)

			ginkgo.By(fmt.Sprintf("labelling node %q with %q", targetNode.Name, nodeLabel))
			gomega.Eventually(func() error {
				nd, err := cs.CoreV1Interface.Nodes().Get(ctx, targetNode.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				nd = nd.DeepCopy()
				gomega.Expect(nd.Labels).ToNot(gomega.HaveKey(nodeLabel), "node %q has label %q too early", nd.Name, nodeLabel)

				nd.Labels[nodeLabel] = ""
				_, err = cs.CoreV1Interface.Nodes().Update(ctx, nd, metav1.UpdateOptions{})
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))
			// feedback for debugging
			tmpNd, err := cs.CoreV1Interface.Nodes().Get(ctx, targetNode.Name, metav1.GetOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred(), "cannot get feedback about %q", targetNode.Name)
			util.Logf("labels on %q after update: %v", tmpNd.Name, tmpNd.Labels)

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
				recommended, err := getRecommendedProfile(verifData.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			gomega.Eventually(func() error {
				curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				ginkgo.By(fmt.Sprintf("checking conditions for profile %q: %#v", curProf.Name, curProf.Status.Conditions))
				if len(curProf.Status.Conditions) == 0 {
					return fmt.Errorf("missing status conditions")
				}
				for _, condition := range curProf.Status.Conditions {
					if condition.Type == tunedv1.TunedProfileApplied && condition.Status != corev1.ConditionTrue {
						return fmt.Errorf("Profile deferred=%v %s not applied", ntoutil.GetDeferredUpdateAnnotation(curProf.Annotations), curProf.Name)
					}
					if condition.Type == tunedv1.TunedDegraded && condition.Status != corev1.ConditionFalse {
						return fmt.Errorf("Profile deferred=%v %s degraded", ntoutil.GetDeferredUpdateAnnotation(curProf.Annotations), curProf.Name)
					}
				}
				ginkgo.By(fmt.Sprintf("checking configuration from profile %q was applied", curProf.Name))
				out, err := util.ExecCmdInPod(verifData.TargetTunedPod, verifData.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifData.OutputExpected {
					return fmt.Errorf("got: %s; expected: %s", out, verifData.OutputExpected)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
		})
	})
})
