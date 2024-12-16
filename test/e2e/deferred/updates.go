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

/*
 * notes about manifests to be used in this test:
 * - manifests are already annotated natively (no need to call setDeferred)
 * - we need fresh manifests never seen before in the node; this is why we also
 *   set the annoation natively. This restriction many be lifted in the future.
 */
var _ = ginkgo.Describe("Profile deferred", ginkgo.Label("deferred", "inplace-update"), func() {
	ginkgo.Context("when applied", func() {
		var (
			createdTuneds     []string
			referenceNode     *corev1.Node // control plane
			targetNode        *corev1.Node
			referenceTunedPod *corev1.Pod // control plane
			referenceProfile  string

			dirPath string
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
		})

		ginkgo.AfterEach(func() {
			// Ignore failures to cleanup resources which are already deleted or not yet created.
			for _, createdTuned := range createdTuneds {
				ginkgo.By(fmt.Sprintf("cluster changes rollback: %q", createdTuned))
				_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", createdTuned)
			}

			checkWorkerNodeIsDefaultState(context.Background(), targetNode)
		})

		ginkgo.It("should trigger changes when applied fist, then deferred when edited", func(ctx context.Context) {
			tunedPathBootstrap := filepath.Join(dirPath, tunedVMLatInplaceBootstrap)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPathBootstrap, dirPath))
			tunedPathUpdate := filepath.Join(dirPath, tunedVMLatInplaceUpdate)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPathUpdate, dirPath))

			tunedBootstrap, err := util.LoadTuned(tunedPathBootstrap)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))

			tunedUpdate, err := util.LoadTuned(tunedPathUpdate)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedUpdate.Name, ntoutil.GetDeferredUpdateAnnotation(tunedUpdate.Annotations)))

			verifDataBootstrap := util.MustExtractVerificationOutputAndCommand(cs, targetNode, tunedBootstrap)
			gomega.Expect(verifDataBootstrap.OutputCurrent).ToNot(gomega.Equal(verifDataBootstrap.OutputExpected), "current output %q already matches expected %q", verifDataBootstrap.OutputCurrent, verifDataBootstrap.OutputExpected)

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedBootstrap, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathBootstrap) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedBootstrap.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedBootstrap.Name)
			gomega.Expect(tunedBootstrap.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedBootstrap.Name)
			expectedProfile := *tunedBootstrap.Spec.Recommend[0].Profile
			ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

			curProfName := ""
			ginkgo.By(fmt.Sprintf("waiting for tuned object %s deferred=%v to be in effect", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
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
				recommended, err := getRecommendedProfile(verifDataBootstrap.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				curProfName = recommended
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v are applied", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Eventually(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q are changed as expected [command=%v]", curProfName, verifDataBootstrap.CommandArgs))
				out, err := util.ExecCmdInPod(verifDataBootstrap.TargetTunedPod, verifDataBootstrap.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifDataBootstrap.OutputExpected {
					return fmt.Errorf("got: %s; expected: %s", out, verifDataBootstrap.OutputExpected)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v changes stay applied", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Consistently(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q are changed as expected [command=%v]", curProfName, verifDataBootstrap.CommandArgs))
				out, err := util.ExecCmdInPod(verifDataBootstrap.TargetTunedPod, verifDataBootstrap.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifDataBootstrap.OutputExpected {
					return fmt.Errorf("got: %s; expected: %s", out, verifDataBootstrap.OutputExpected)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("updating tuned object %s deferred=%v in place", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Eventually(func() error {
				curTuned, err := cs.Tuneds(ntoconfig.WatchNamespace()).Get(ctx, tunedBootstrap.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				curTuned = curTuned.DeepCopy()
				curTuned.Spec = tunedUpdate.Spec
				_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Update(ctx, curTuned, metav1.UpdateOptions{})
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v is now NOT picked up and stays deferred, being in-place update", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Eventually(func() error {
				curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

				if len(curProf.Status.Conditions) == 0 {
					return fmt.Errorf("missing status conditions")
				}
				ginkgo.By(fmt.Sprintf("checking UPDATED conditions for profile %q: %#v", curProf.Name, curProf.Status.Conditions))

				cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
				if cond == nil {
					return fmt.Errorf("missing status applied condition")
				}

				ginkgo.By(fmt.Sprintf("checking UPDATED condition is DEFERRED: %#v", cond))
				err = checkAppliedConditionDeferred(cond, expectedProfile)
				if err != nil {
					util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					return err
				}
				recommended, err := getRecommendedProfile(verifDataBootstrap.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v INITIAL changes stay applied", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Consistently(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q are changed as expected [command=%v]", curProfName, verifDataBootstrap.CommandArgs))
				out, err := util.ExecCmdInPod(verifDataBootstrap.TargetTunedPod, verifDataBootstrap.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifDataBootstrap.OutputExpected {
					return fmt.Errorf("got: %s; expected: %s", out, verifDataBootstrap.OutputExpected)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			// on non-targeted nodes, like control plane, nothing should have changed
			checkAppliedConditionStaysOKForNode(ctx, referenceNode.Name, referenceProfile)
		})

		ginkgo.It("should trigger changes when applied fist, then deferred when edited, if tuned restart should be kept deferred", ginkgo.Label("reload", "flaky"), func(ctx context.Context) {
			tunedPathBootstrap := filepath.Join(dirPath, tunedVMLatInplaceBootstrap)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPathBootstrap, dirPath))
			tunedPathUpdate := filepath.Join(dirPath, tunedVMLatInplaceUpdate)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPathUpdate, dirPath))

			tunedBootstrap, err := util.LoadTuned(tunedPathBootstrap)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))

			tunedUpdate, err := util.LoadTuned(tunedPathUpdate)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tunedUpdate.Name, ntoutil.GetDeferredUpdateAnnotation(tunedUpdate.Annotations)))

			verifDataBootstrap := util.MustExtractVerificationOutputAndCommand(cs, targetNode, tunedBootstrap)
			gomega.Expect(verifDataBootstrap.OutputCurrent).ToNot(gomega.Equal(verifDataBootstrap.OutputExpected), "current output %q already matches expected %q", verifDataBootstrap.OutputCurrent, verifDataBootstrap.OutputExpected)

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tunedBootstrap, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			createdTuneds = prepend(createdTuneds, tunedPathBootstrap) // we need the path, not the name
			ginkgo.By(fmt.Sprintf("create tuneds: %v", createdTuneds))

			gomega.Expect(tunedBootstrap.Spec.Recommend).ToNot(gomega.BeEmpty(), "tuned %q has empty recommendations", tunedBootstrap.Name)
			gomega.Expect(tunedBootstrap.Spec.Recommend[0].Profile).ToNot(gomega.BeNil(), "tuned %q has empty recommended tuned profile", tunedBootstrap.Name)
			expectedProfile := *tunedBootstrap.Spec.Recommend[0].Profile
			ginkgo.By(fmt.Sprintf("expecting Tuned Profile %q to be picked up", expectedProfile))

			curProfName := ""
			ginkgo.By(fmt.Sprintf("waiting for tuned object %s deferred=%v to be in effect", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
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
				recommended, err := getRecommendedProfile(verifDataBootstrap.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				curProfName = recommended
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v are applied", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Eventually(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q are changed as expected [command=%v]", curProfName, verifDataBootstrap.CommandArgs))
				out, err := util.ExecCmdInPod(verifDataBootstrap.TargetTunedPod, verifDataBootstrap.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifDataBootstrap.OutputExpected {
					return fmt.Errorf("got: %s; expected: %s", out, verifDataBootstrap.OutputExpected)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("updating tuned object %s deferred=%v in place", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Eventually(func() error {
				curTuned, err := cs.Tuneds(ntoconfig.WatchNamespace()).Get(ctx, tunedBootstrap.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				curTuned = curTuned.DeepCopy()
				curTuned.Spec = tunedUpdate.Spec
				_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Update(ctx, curTuned, metav1.UpdateOptions{})
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v is now NOT picked up and stays deferred, being in-place update", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Eventually(func() error {
				curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

				if len(curProf.Status.Conditions) == 0 {
					return fmt.Errorf("missing status conditions")
				}
				ginkgo.By(fmt.Sprintf("checking UPDATED conditions for profile %q: %#v", curProf.Name, curProf.Status.Conditions))

				cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
				if cond == nil {
					return fmt.Errorf("missing status applied condition")
				}

				ginkgo.By(fmt.Sprintf("checking UPDATED condition is DEFERRED: %#v", cond))
				err = checkAppliedConditionDeferred(cond, expectedProfile)
				if err != nil {
					util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					return err
				}
				recommended, err := getRecommendedProfile(verifDataBootstrap.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v INITIAL changes stay applied", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Consistently(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q are changed as expected [command=%v]", curProfName, verifDataBootstrap.CommandArgs))
				out, err := util.ExecCmdInPod(verifDataBootstrap.TargetTunedPod, verifDataBootstrap.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifDataBootstrap.OutputExpected {
					return fmt.Errorf("got: %s; expected: %s", out, verifDataBootstrap.OutputExpected)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			oldTunedPodUID := verifDataBootstrap.TargetTunedPod.UID
			ginkgo.By(fmt.Sprintf("killing the tuned pod running on %q", targetNode.Name))
			gomega.Expect(cs.Pods(verifDataBootstrap.TargetTunedPod.Namespace).Delete(ctx, verifDataBootstrap.TargetTunedPod.Name, metav1.DeleteOptions{})).To(gomega.Succeed())
			// wait for the tuned pod to be found again

			var targetTunedPod *corev1.Pod
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

			verifDataBootstrap.TargetTunedPod = targetTunedPod

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v is again NOT picked up and stays deferred, being in-place update", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Eventually(func() error {
				curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, targetNode.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", curProf.Name, expectedProfile))

				if len(curProf.Status.Conditions) == 0 {
					return fmt.Errorf("missing status conditions")
				}
				ginkgo.By(fmt.Sprintf("checking UPDATED conditions for profile %q: %#v", curProf.Name, curProf.Status.Conditions))

				cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
				if cond == nil {
					return fmt.Errorf("missing status applied condition")
				}

				ginkgo.By(fmt.Sprintf("checking UPDATED condition is DEFERRED: %#v", cond))
				err = checkAppliedConditionDeferred(cond, expectedProfile)
				if err != nil {
					util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
					return err
				}
				recommended, err := getRecommendedProfile(verifDataBootstrap.TargetTunedPod)
				if err != nil {
					return err
				}
				if recommended != expectedProfile {
					return fmt.Errorf("recommended profile is %q expected %q", recommended, expectedProfile)
				}
				return err
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			ginkgo.By(fmt.Sprintf("ensuring tuned object %s deferred=%v INITIAL changes are still applied", tunedBootstrap.Name, ntoutil.GetDeferredUpdateAnnotation(tunedBootstrap.Annotations)))
			gomega.Consistently(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q are changed as expected [command=%v]", curProfName, verifDataBootstrap.CommandArgs))
				out, err := util.ExecCmdInPod(verifDataBootstrap.TargetTunedPod, verifDataBootstrap.CommandArgs...)
				if err != nil {
					return err
				}
				out = strings.TrimSpace(out)
				if out != verifDataBootstrap.OutputExpected {
					return fmt.Errorf("got: %s; expected: %s", out, verifDataBootstrap.OutputExpected)
				}
				return nil
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			// on non-targeted nodes, like control plane, nothing should have changed
			checkAppliedConditionStaysOKForNode(ctx, referenceNode.Name, referenceProfile)
		})
	})
})
