package ready

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

func Node(node corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func WaitNodeOrFail(cs *framework.ClientSet, tag, nodeName string, timeout, polling time.Duration) {
	ginkgo.GinkgoHelper()

	util.Logf("%s: waiting for node %q: to be NOT-ready", tag, nodeName)
	gomega.Eventually(func() (bool, error) {
		node, err := cs.CoreV1Interface.Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			// intentionally tolerate error
			util.Logf("wait for node %q ready: %v", nodeName, err)
			return false, nil
		}
		ready := Node(*node)
		util.Logf("node %q ready=%v", nodeName, ready)
		return !ready, nil // note "not"
	}).WithTimeout(2*time.Minute).WithPolling(polling).Should(gomega.BeTrue(), "post reboot/1: cannot get readiness status after reboot for node %q", nodeName)

	util.Logf("%s: waiting for node %q: to be ready", tag, nodeName)
	gomega.Eventually(func() (bool, error) {
		node, err := cs.CoreV1Interface.Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			// intentionally tolerate error
			util.Logf("wait for node %q ready: %v", nodeName, err)
			return false, nil
		}
		ready := Node(*node)
		util.Logf("node %q ready=%v", nodeName, ready)
		return ready, nil
	}).WithTimeout(timeout).WithPolling(polling).Should(gomega.BeTrue(), "post reboot/2: cannot get readiness status after reboot for node %q", nodeName)

	util.Logf("%s: node %q: reported ready", tag, nodeName)
}
