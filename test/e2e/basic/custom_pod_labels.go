package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the application (and rollback) of a custom profile via Pod labelling.
var _ = ginkgo.Describe("[basic][custom_pod_labels] Node Tuning Operator custom profile, Pod labels", func() {
	const (
		profileIngress  = "../../../examples/ingress.yaml"
		podLabelIngress = "tuned.openshift.io/ingress"
		sysctlVar       = "net.ipv4.tcp_tw_reuse"
	)

	ginkgo.Context("custom profile: Pod labels", func() {
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
			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node := nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err = util.GetTunedForNode(cs, &node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the current value of %s in Pod %s", sysctlVar, pod.Name))
			valOrig, err := util.WaitForSysctlInPod(pollInterval, waitDuration, pod, sysctlVar)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling Pod %s with label %s", pod.Name, podLabelIngress))
			_, _, err = util.ExecAndLogCommand("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelIngress+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom ingress profile %s", profileIngress))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileIngress)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("ensuring the custom worker node profile was set")
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, "1")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from Pod %s", podLabelIngress, pod.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelIngress+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the original %s value (%s) is set in Pod %s", sysctlVar, valOrig, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, valOrig)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom ingress profile %s", profileIngress))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileIngress)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
