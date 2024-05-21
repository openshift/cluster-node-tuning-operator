package e2e

import (
	"context"
	"encoding/json"
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

const (
	verifyCommandAnnotation = "verificationCommand"
	verifyOutputAnnotation  = "verificationOutput"

	pollInterval = 5 * time.Second
	waitDuration = 5 * time.Minute
	// The number of Profile status conditions.  Adjust when adding new conditions in the API.
	ProfileStatusConditions = 2

	tunedSHMMNI    = "../testing_manifests/deferred/tuned-basic-00.yaml"
	tunedCPUEnergy = "../testing_manifests/deferred/tuned-basic-10.yaml"
)

var _ = ginkgo.Describe("[deferred][profile_status] Profile deferred", func() {
	ginkgo.Context("when applied", func() {
		var (
			createdTuneds  []string
			targetNode     *corev1.Node
			targetTunedPod *corev1.Pod
		)

		ginkgo.BeforeEach(func() {
			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			targetNode = &nodes[0]
			ginkgo.By(fmt.Sprintf("using node %q as reference", targetNode.Name))

			targetTunedPod, err = util.GetTunedForNode(cs, targetNode)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			createdTuneds = []string{}
		})

		ginkgo.AfterEach(func() {
			for _, createdTuned := range createdTuneds {
				ginkgo.By(fmt.Sprintf("cluster changes rollback: %q", createdTuned))
				util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", createdTuned)
			}
		})

		ginkgo.It("should not trigger any actual change", func(ctx context.Context) {
			dirPath, err := getCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPath := filepath.Join(dirPath, tunedSHMMNI)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPath, dirPath))

			tuned, err := loadTuned(tunedPath)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			verificationCommand, ok := tuned.Annotations[verifyCommandAnnotation]
			gomega.Expect(ok).To(gomega.BeTrue(), "missing verification command annotation %s", verifyCommandAnnotation)

			verificationCommandArgs := []string{}
			err = json.Unmarshal([]byte(verificationCommand), &verificationCommandArgs)
			gomega.Expect(verificationCommandArgs).ToNot(gomega.BeEmpty(), "missing verification command args")
			ginkgo.By(fmt.Sprintf("verification command: %v", verificationCommandArgs))

			// note we must ignore the annotation since we do NOT want this tuned to be applied
			verificationOutput, err := util.ExecCmdInPod(targetTunedPod, verificationCommandArgs...)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			verificationOutput = strings.TrimSpace(verificationOutput)
			ginkgo.By(fmt.Sprintf("verification expected output: %q", verificationOutput))

			tunedMutated := setDeferred(tuned.DeepCopy())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated.Annotations)))

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPath) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tuned.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tuned.Name)
			gomega.Expect(tuned.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tuned.Name)
			expectedProfile := *tuned.Spec.Recommend[0].Profile
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
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q", curProf.Name))
				out, err := util.ExecCmdInPod(targetTunedPod, verificationCommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verificationOutput {
					return fmt.Errorf("got: %s; expected: %s", out, verificationOutput)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(2 * time.Minute).Should(gomega.Succeed())
		})

		ginkgo.It("should revert the profile status on removal", func(ctx context.Context) {
			dirPath, err := getCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPath := filepath.Join(dirPath, tunedSHMMNI)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPath, dirPath))

			tuned, err := loadTuned(tunedPath)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedMutated := setDeferred(tuned.DeepCopy())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated.Annotations)))

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPath) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tuned.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tuned.Name)
			gomega.Expect(tuned.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tuned.Name)
			expectedProfile := *tuned.Spec.Recommend[0].Profile
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
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
		})

		ginkgo.It("should be overridden by another deferred update", func(ctx context.Context) {
			dirPath, err := getCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPathSHMMNI := filepath.Join(dirPath, tunedSHMMNI)
			tunedDeferred, err := loadTuned(tunedPathSHMMNI)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedMutated := setDeferred(tunedDeferred.DeepCopy())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated.Annotations)))

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathSHMMNI) // we need the path, not the name
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

			tunedPathCPUEnergy := filepath.Join(dirPath, tunedCPUEnergy)
			tunedDeferred2, err := loadTuned(tunedPathCPUEnergy)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedMutated2 := setDeferred(tunedDeferred2.DeepCopy())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated2.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated2.Annotations)))

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated2, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathCPUEnergy) // we need the path, not the name
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
				return err
			}).WithPolling(10 * time.Second).WithTimeout(5 * time.Minute).Should(gomega.Succeed())
		})

		ginkgo.It("should be overridden by a immediate update by edit", func(ctx context.Context) {
			dirPath, err := getCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPathCPUEnergy := filepath.Join(dirPath, tunedCPUEnergy)
			tunedImmediate, err := loadTuned(tunedPathCPUEnergy)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedMutated := setDeferred(tunedImmediate.DeepCopy())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedMutated.Name, ntoutil.HasDeferredUpdateAnnotation(tunedMutated.Annotations)))

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedMutated, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathCPUEnergy) // we need the path, not the name
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

			gomega.Eventually(func() error {
				curTuned, err := cs.Tuneds(ntoconfig.WatchNamespace()).Get(ctx, tunedImmediate.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				curTuned = curTuned.DeepCopy()

				ginkgo.By(fmt.Sprintf("removing the deferred annotation from Tuned %q", tunedImmediate.Name))
				curTuned.Annotations = ntoutil.ToggleDeferredUpdateAnnotation(curTuned.Annotations, false)

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
	})
})
