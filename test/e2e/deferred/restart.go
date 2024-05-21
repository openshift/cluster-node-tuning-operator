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

var _ = ginkgo.Describe("[deferred][restart] Profile deferred", func() {
	ginkgo.Context("on tuned restart", func() {
		var (
			createdTuneds []string
			targetNode    *corev1.Node
		)

		ginkgo.BeforeEach(func() {
			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			targetNode = &nodes[0]
			ginkgo.By(fmt.Sprintf("using node %q as reference", targetNode.Name))

			createdTuneds = []string{}
		})

		ginkgo.AfterEach(func() {
			for _, createdTuned := range createdTuneds {
				ginkgo.By(fmt.Sprintf("cluster changes rollback: %q", createdTuned))
				util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", createdTuned)
			}
		})

		ginkgo.It("should be applied", func(ctx context.Context) {
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

			ginkgo.By(fmt.Sprintf("getting the tuned pod running on %q", targetNode.Name))
			targetTunedPod, err := util.GetTunedForNode(cs, targetNode)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("got the tuned pod running on %q: %s/%s %s", targetNode.Name, targetTunedPod.Namespace, targetTunedPod.Name, targetTunedPod.UID))

			ginkgo.By(fmt.Sprintf("killing the tuned pod running on %q", targetNode.Name))
			gomega.Expect(cs.Pods(targetTunedPod.GetNamespace()).Delete(ctx, targetTunedPod.Name, metav1.DeleteOptions{})).To(gomega.Succeed())

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

			verifications := extractVerifications(tunedImmediate)
			gomega.Expect(len(verifications)).To(gomega.Equal(1), "unexpected verification count, check annotations")

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
