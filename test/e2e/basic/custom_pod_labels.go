package e2e

import (
	"fmt"
	"os/exec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/openshift/cluster-node-tuning-operator/test/e2e/utils"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	coreapi "k8s.io/api/core/v1"
)

// Test the application (and rollback) of a custom profile via pod labelling.
var _ = Describe("[basic][custom_pod_labels] Node Tuning Operator custom profile, pod labels", func() {
	const (
		profileElasticSearch  = "../../../examples/elasticsearch.yaml"
		podLabelElasticSearch = "tuned.openshift.io/elasticsearch"
		sysctlVar             = "vm.max_map_count"
	)

	Context("custom profile: pod labels", func() {
		var (
			pod *coreapi.Pod
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of It()
		AfterEach(func() {
			By("cluster changes rollback")
			if pod != nil {
				exec.Command("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelElasticSearch+"-").CombinedOutput()
			}
			exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileElasticSearch).CombinedOutput()
		})

		It(fmt.Sprintf("%s set", sysctlVar), func() {
			By("getting a list of worker nodes")
			nodes, err := GetNodesByRole(cs, "worker")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(nodes)).NotTo(BeZero())

			node := nodes[0]
			By(fmt.Sprintf("getting a tuned pod running on node %s", node.Name))
			pod, err = GetTunedForNode(cs, &node)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("getting the current value of %s in pod %s", sysctlVar, pod.Name))
			valOrig, err := GetSysctl(sysctlVar, pod)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("labelling pod %s with label %s", pod.Name, podLabelElasticSearch))
			_, err = exec.Command("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelElasticSearch+"=").CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating the custom elasticsearch profile %s", profileElasticSearch))
			_, err = exec.Command("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileElasticSearch).CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the custom worker node profile was set")
			err = EnsureSysctl(pod, sysctlVar, "262144")
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("removing label %s from pod %s", podLabelElasticSearch, pod.Name))
			_, err = exec.Command("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelElasticSearch+"-").CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("ensuring the original %s value (%s) is set in pod %s", sysctlVar, valOrig, pod.Name))
			err = EnsureSysctl(pod, sysctlVar, valOrig)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("deleting the custom elasticsearch profile %s", profileElasticSearch))
			_, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileElasticSearch).CombinedOutput()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
