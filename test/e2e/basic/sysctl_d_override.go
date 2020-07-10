package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the application (and rollback) of host's /etc/sysctl.d/*.conf override.
var _ = ginkgo.Describe("[basic][sysctl_d_override] Node Tuning Operator /etc/sysctl.d/*.conf override", func() {
	const (
		sysctlVar    = "net.ipv4.neigh.default.gc_thresh1"
		sysctlValSet = "256"
		sysctlFile   = "/host/etc/sysctl.d/zzz.conf"
	)

	ginkgo.Context("sysctl.d override", func() {
		var (
			pod *coreapi.Pod
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")

			if pod != nil {
				util.ExecAndLogCommand("oc", "exec", "-n", ntoconfig.OperatorNamespace(), pod.Name, "--", "rm", sysctlFile)
			}
		})

		ginkgo.It(fmt.Sprintf("%s set", sysctlVar), func() {
			var explain string

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node := &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a tuned pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the current value of %s in pod %s", sysctlVar, pod.Name))
			valOrig, err := util.GetSysctl(sysctlVar, pod)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("writing %s override file on the host with %s=%s", sysctlFile, sysctlVar, sysctlValSet))
			_, _, err = util.ExecAndLogCommand("oc", "exec", "-n", ntoconfig.OperatorNamespace(), pod.Name, "--", "sh", "-c",
				fmt.Sprintf("echo %s=%s > %s", sysctlVar, sysctlValSet, sysctlFile))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting pod %s", pod.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "pod", pod.Name, "--wait")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for a new tuned pod to be ready on node %s", node.Name))
			err = wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
				pod, err = util.GetTunedForNode(cs, node)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			ginkgo.By(fmt.Sprintf("ensuring new %s value (%s) is set in pod %s", sysctlVar, sysctlValSet, pod.Name))
			err = util.EnsureSysctl(pod, sysctlVar, sysctlValSet)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing %s override file on the host", sysctlFile))
			_, _, err = util.ExecAndLogCommand("oc", "exec", "-n", ntoconfig.OperatorNamespace(), pod.Name, "--", "rm", sysctlFile)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting pod %s", pod.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "pod", pod.Name, "--wait")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for a new tuned pod to be ready on node %s", node.Name))
			err = wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
				pod, err = util.GetTunedForNode(cs, node)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			ginkgo.By(fmt.Sprintf("ensuring the original %s value (%s) is set in pod %s", sysctlVar, valOrig, pod.Name))
			err = util.EnsureSysctl(pod, sysctlVar, valOrig)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
