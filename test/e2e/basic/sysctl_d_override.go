package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the application (and rollback) of host's /etc/sysctl.d/*.conf override.
var _ = ginkgo.Describe("[basic][sysctl_d_override] Node Tuning Operator /etc/sysctl.d/*.conf override", func() {
	const (
		sysctlVar               = "net.ipv4.neigh.default.gc_thresh1"
		sysctlValSet            = "256"
		sysctlFile              = "/host/etc/sysctl.d/zzz.conf"
		profileSysctlOverride   = "../testing_manifests/sysctl_override.yaml"
		nodeLabelSysctlOverride = "tuned.openshift.io/sysctl-override"
	)

	ginkgo.Context("sysctl.d override", func() {
		var (
			node *coreapi.Node
			pod  *coreapi.Pod
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")

			if node != nil {
				util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelSysctlOverride+"-")
			}
			if pod != nil {
				util.ExecAndLogCommand("oc", "exec", "-n", ntoconfig.OperatorNamespace(), pod.Name, "--", "rm", sysctlFile)
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileSysctlOverride)
		})

		ginkgo.It(fmt.Sprintf("%s set", sysctlVar), func() {
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

			ginkgo.By(fmt.Sprintf("getting the current value of %s in Pod %s", sysctlVar, pod.Name))
			valOrig, err := util.WaitForSysctlInPod(pollInterval, waitDuration, pod, sysctlVar)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("writing %s override file on the host with %s=%s", sysctlFile, sysctlVar, sysctlValSet))
			_, _, err = util.ExecAndLogCommand("oc", "exec", "-n", ntoconfig.OperatorNamespace(), pod.Name, "--", "sh", "-c",
				fmt.Sprintf("echo %s=%s > %s; sync %s", sysctlVar, sysctlValSet, sysctlFile, sysctlFile))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			util.ExecAndLogCommand("oc", "rsh", "-n", ntoconfig.OperatorNamespace(), pod.Name, "cat", sysctlFile)

			ginkgo.By(fmt.Sprintf("deleting Pod %s", pod.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "pod", pod.Name, "--wait")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for a new TuneD Pod to be ready on node %s", node.Name))
			err = wait.PollImmediate(pollInterval, waitDuration, func() (bool, error) {
				pod, err = util.GetTunedForNode(cs, node)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			ginkgo.By(fmt.Sprintf("ensuring new %s value (%s) is set in Pod %s", sysctlVar, sysctlValSet, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, sysctlValSet)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelSysctlOverride))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelSysctlOverride+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", profileSysctlOverride))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileSysctlOverride)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("ensuring the custom worker node profile was set")
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, "8192") // 8192 comes from the default openshift profile
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelSysctlOverride, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelSysctlOverride+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", profileSysctlOverride))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileSysctlOverride)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing %s override file on the host", sysctlFile))
			_, _, err = util.ExecAndLogCommand("oc", "exec", "-n", ntoconfig.OperatorNamespace(), pod.Name, "--", "rm", sysctlFile)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting Pod %s", pod.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "pod", pod.Name, "--wait")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for a new TuneD Pod to be ready on node %s", node.Name))
			err = wait.PollImmediate(pollInterval, waitDuration, func() (bool, error) {
				pod, err = util.GetTunedForNode(cs, node)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			ginkgo.By(fmt.Sprintf("ensuring the original %s value (%s) is set in Pod %s", sysctlVar, valOrig, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, valOrig)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
