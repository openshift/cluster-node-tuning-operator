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

// Test the functionality of the preStop container lifecycle hook -- TuneD settings rollback.
var _ = ginkgo.Describe("[basic][rollback] Node Tuning Operator settings rollback", func() {
	const (
		profileIngress  = "../../../examples/ingress.yaml"
		podLabelIngress = "tuned.openshift.io/ingress"
		sysctlVar       = "net.ipv4.tcp_tw_reuse"
		sysctlValDef    = "2" // default value of 'sysctlVar'
	)

	ginkgo.Context("TuneD settings rollback", func() {
		var (
			pod *coreapi.Pod
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")
			if pod != nil {
				util.ExecAndLogCommand("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelIngress+"-")
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileIngress)
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

			node := nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err = util.GetTunedForNode(cs, &node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Expect the default worker node profile applied prior to getting any current values.
			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", util.DefaultWorkerProfile, node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, util.DefaultWorkerProfile, tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the default %s value (%s) is set in Pod %s", sysctlVar, sysctlValDef, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, sysctlValDef)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling Pod %s with label %s", pod.Name, podLabelIngress))
			_, _, err = util.ExecAndLogCommand("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelIngress+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating custom profile %s", profileIngress))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileIngress)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("ensuring the custom worker node profile was set")
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, "1")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting Pod %s", pod.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "pod", pod.Name, "--wait")
			pod = nil
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for a new TuneD Pod to be ready on node %s", node.Name))
			err = wait.PollImmediate(pollInterval, waitDuration, func() (bool, error) {
				pod, err = util.GetTunedForNode(cs, &node)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			ginkgo.By(fmt.Sprintf("ensuring the default %s value (%s) is set in Pod %s", sysctlVar, sysctlValDef, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, sysctlValDef)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting custom profile %s", profileIngress))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileIngress)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
