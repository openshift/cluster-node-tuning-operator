package e2e

import (
	"context"
	"fmt"
	"path/filepath"
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

var _ = ginkgo.Describe("[deferred][non_regression] Profile non-deferred", func() {
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

		ginkgo.It("should trigger changes", func(ctx context.Context) {
			dirPath, err := getCurrentDirPath()
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			tunedPath := filepath.Join(dirPath, tunedCPUEnergy)
			ginkgo.By(fmt.Sprintf("loading tuned data from %s (basepath=%s)", tunedPath, dirPath))

			tuned, err := loadTuned(tunedPath)
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("creating tuned object %s deferred=%v", tuned.Name, ntoutil.HasDeferredUpdateAnnotation(tuned.Annotations)))

			verifications := extractVerifications(tuned)
			gomega.Expect(len(verifications)).To(gomega.Equal(1), "unexpected verification count, check annotations")

			_, err = cs.Tuneds(ntoconfig.WatchNamespace()).Create(ctx, tuned, metav1.CreateOptions{})
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
				err = checkAppliedConditionOK(cond)
				if err != nil {
					util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
				}
				return verify(targetTunedPod, verifications)
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())

			gomega.Consistently(func() error {
				ginkgo.By(fmt.Sprintf("checking real node conditions for profile %q", targetNode.Name))
				return verify(targetTunedPod, verifications)
			}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
		})
	})
})
