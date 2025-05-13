package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

var _ = ginkgo.Describe("[basic][startup_udev_settle_wait] Node Tuning Operator /etc/tuned/tuned-main.conf startup_udev_settle_wait setting", func() {
	const (
		tunedMainConfVar        = "startup_udev_settle_wait"
		tunedMainConfPath       = "/etc/tuned/tuned-main.conf"
		profileUdevSettleWait   = "../testing_manifests/udev_settle_wait.yaml"
		nodeLabelUdevSettleWait = "tuned.openshift.io/udev-settle-wait"
	)

	ginkgo.Context("startup_udev_settle_wait setting", func() {
		var (
			node *coreapi.Node
			pod  *coreapi.Pod
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			// Ignore failures to cleanup resources which are already deleted or not yet created.
			ginkgo.By("cluster changes rollback")

			if node != nil {
				_, _, _ = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelUdevSettleWait+"-")
			}
			_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileUdevSettleWait)
		})

		ginkgo.It(fmt.Sprintf("%s set", tunedMainConfVar), func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)
			var explain string

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err = util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Expect the default worker node profile applied prior to getting any current values.
			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", util.GetDefaultWorkerProfile(node), node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, util.GetDefaultWorkerProfile(node), tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			getOptionInTunedMainConf := func(opt string) (string, error) {
				out, err := util.ExecCmdInPod(pod, "sed", "-En", `s/^(\s*`+opt+`\s*=\s*)(\d*)/\2/p`, tunedMainConfPath)
				out = strings.TrimSpace(out)
				return out, err
			}

			ginkgo.By(fmt.Sprintf("getting the default value of startup_udev_settle_wait in %s", tunedMainConfPath))
			out, err := getOptionInTunedMainConf(tunedMainConfVar)
			valOrig := out
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelUdevSettleWait))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelUdevSettleWait+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", profileUdevSettleWait))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", profileUdevSettleWait)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for %s=10 in %s", tunedMainConfVar, tunedMainConfPath))
			err = wait.PollUntilContextTimeout(context.TODO(), pollInterval, waitDuration, true, func(ctx context.Context) (bool, error) {
				out, err := getOptionInTunedMainConf(tunedMainConfVar)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				if out != "10" {
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelUdevSettleWait, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelUdevSettleWait+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", profileUdevSettleWait))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileUdevSettleWait)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for %s=%s in %s", tunedMainConfVar, valOrig, tunedMainConfPath))
			err = wait.PollUntilContextTimeout(context.TODO(), pollInterval, waitDuration, true, func(ctx context.Context) (bool, error) {
				out, err := getOptionInTunedMainConf(tunedMainConfVar)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				if out != valOrig {
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)
		})
	})
})
