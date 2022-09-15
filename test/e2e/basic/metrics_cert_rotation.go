package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

var _ = ginkgo.Describe("[basic][metrics] Node Tuning Operator certificate rotation", func() {
	ginkgo.Context("TLS certificate rotation", func() {
		ginkgo.It("delete certificate Secret and check that the server restarts with latest certificate", func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)

			// Delete the existing secret to force certificate rotation.
			ginkgo.By("deleting node-tuning-operator-tls Secret to trigger certificate rotation")
			err := rotateTLSCertSecret()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Delete the existing secret again to force certificate rotation.
			// This is done twice to test whether the file watcher in the metrics server
			// will continue to watch the new certificates for changes after a rotation.
			ginkgo.By("deleting node-tuning-operator-tls Secret a second time")
			err = rotateTLSCertSecret()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node := &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			tunedPod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("getting cluster-node-tuning-operator Pod")
			operatorPod, err := util.GetNodeTuningOperatorPod(cs)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("checking if server TLS certificate matches TLS certificate in Secret")
			err = wait.PollImmediate(pollInterval, waitDuration, func() (bool, error) {
				tlsSecret, err := cs.Secrets(ntoconfig.WatchNamespace()).Get(context.TODO(), "node-tuning-operator-tls", metav1.GetOptions{})
				if err != nil {
					util.Logf("error getting secret/node-tuning-operator-tls. May not exist yet. Err: %v", err)
					return false, nil
				}
				secretCertContents := string(tlsSecret.Data["tls.crt"])

				operatorPodIP := operatorPod.Status.PodIP
				// We need chroot because host may be using system libraries incompatible with the container
				// image system libraries.  Alternatively, use container-shipped openssl.
				opensslCmd := "/usr/sbin/chroot /host /usr/bin/openssl s_client -connect " + operatorPodIP + ":60000 2>/dev/null </dev/null | sed -ne '/-BEGIN CERTIFICATE-/,/-END CERTIFICATE-/p'"

				serverCertContents, err := util.ExecCmdInPod(tunedPod, "/bin/bash", "-c", opensslCmd)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

				if len(serverCertContents) > 0 && strings.Contains(secretCertContents, serverCertContents) {
					return true, nil
				}
				return false, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})

// Deletes the secret/node-tuning-operator-tls and
// waits up to 2 minutes for it to be recreated with the new certificate.
func rotateTLSCertSecret() error {
	// Get original Secret.
	tlsSecret, err := cs.Secrets(ntoconfig.WatchNamespace()).Get(context.TODO(), "node-tuning-operator-tls", metav1.GetOptions{})
	if err != nil {
		util.Logf("Error getting secret/node-tuning-operator-tls. May not exist yet. Err: %v", err)
		return err
	}
	origCertContents := string(tlsSecret.Data["tls.crt"])

	_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "secret/node-tuning-operator-tls")
	if err != nil {
		util.Logf("Error deleting secret/node-tuning-operator/tls")
		return err
	}

	// Wait for new certificate to be injected into the Secret.
	err = wait.PollImmediate(2*time.Second, 2*time.Minute, func() (bool, error) {
		tlsSecret, err = cs.Secrets(ntoconfig.WatchNamespace()).Get(context.TODO(), "node-tuning-operator-tls", metav1.GetOptions{})
		if err != nil {
			util.Logf("Error getting secret/node-tuning-operator-tls. May not exist yet. Err: %v", err)
			return false, nil
		}
		latestCertContents := string(tlsSecret.Data["tls.crt"])
		if len(latestCertContents) > 0 && latestCertContents != origCertContents {
			// Secret has been updated.
			return true, nil
		}
		return false, nil
	})
	return err
}
