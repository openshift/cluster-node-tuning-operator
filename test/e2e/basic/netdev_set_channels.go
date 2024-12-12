package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the TuneD daemon functionality to adjust netdev queue count for physical network devices via ethtool.
var _ = ginkgo.Describe("[basic][netdev_set_channels] Node Tuning Operator adjust netdev queue count", func() {
	const (
		profileNetdev   = "../testing_manifests/netdev_set_channels.yaml"
		nodeLabelNetdev = "tuned.openshift.io/netdev-set-queue-count"
	)

	ginkgo.Context("adjust netdev queue count", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			// Ignore failures to cleanup resources which are already deleted or not yet created.
			ginkgo.By("cluster changes rollback")
			if node != nil {
				_, _, _ = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelNetdev+"-")
			}
			_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileNetdev)
		})

		ginkgo.It("adjust netdev queue count for physical network devices via ethtool", func() {
			var (
				phyDev  string
				explain string
			)
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
				chLen        = 2 // the number of multi-purpose channels to set by profileNetdev
			)
			cmdGetPhysicalDevices := []string{"find", "/sys/class/net", "-type", "l", "-not", "-lname", "*virtual*", "-printf", "%f "}

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			util.Logf("found Pod %s running on node %s", pod.Name, node.Name)

			ginkgo.By(fmt.Sprintf("getting a list of physical network devices: %v", cmdGetPhysicalDevices))
			phyDevs, err := util.ExecCmdInPod(pod, cmdGetPhysicalDevices...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			for _, d := range strings.Split(phyDevs, " ") {
				if d == "" {
					continue
				}
				// See if the device 'd' supports querying the channels.
				_, err := util.ExecCmdInPod(pod, "ethtool", "-l", d)
				if err == nil {
					// Found a device that supports querying the channels
					phyDev = d
					break
				}
			}

			if phyDev == "" {
				util.Logf(fmt.Sprintf("no network devices supporting querying channels found on node %s; skipping test", node.Name))
				return
			}

			// Sample output of "ethtool -l <device>"
			//
			// Channel parameters for ens4:
			// Pre-set maximums:
			// RX:             0
			// TX:             0
			// Other:          0
			// Combined:       4
			// Current hardware settings:
			// RX:             0
			// TX:             0
			// Other:          0
			// Combined:       4

			cmdCombinedChannelsCurrent := []string{"bash", "-c",
				fmt.Sprintf("ethtool -l %s | sed -n '/Current hardware settings:/,/Combined:/{s/^Combined:\\s*//p}'", phyDev)}
			cmdCombinedChannelsMax := []string{"bash", "-c",
				fmt.Sprintf("ethtool -l %s | sed -n '/Pre-set maximums:/,/Combined:/{s/^Combined:\\s*//p}'", phyDev)}

			ginkgo.By(fmt.Sprintf("using physical network device %s for testing", phyDev))
			out, err := util.ExecCmdInPod(pod, cmdCombinedChannelsMax...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			channelMaximumCombined, err := strconv.Atoi(strings.TrimSpace(out))
			if err != nil || channelMaximumCombined <= 1 {
				util.Logf(fmt.Sprintf("network device %s does not seem to support querying channels [%v] on node %s; skipping test: %v",
					phyDev, channelMaximumCombined, node.Name, err))
				return
			}

			out, err = util.ExecCmdInPod(pod, cmdCombinedChannelsCurrent...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			channelOrigCombined, err := strconv.Atoi(strings.TrimSpace(out))
			if err != nil {
				util.Logf(fmt.Sprintf("unable to retrieve current multi-purpose channels hardware settings for device %s on %s; skipping test: %v",
					phyDev, node.Name, err))
				return
			}

			// Try to lower the number of multi-purpose channels first.
			ginkgo.By(fmt.Sprintf("ensuring the number of multi-purpose channels is set to 1 for %s on node %s", phyDev, node.Name))
			const errUnableToLowerChannels = "unable to lower the number of multi-purpose channels to 1 for device %s on node %s; skipping test: %v"
			out, err = util.ExecCmdInPod(pod, "bash", "-c", fmt.Sprintf("ethtool -L %s combined 1", phyDev))
			if err != nil {
				util.Logf(errUnableToLowerChannels, phyDev, node.Name, err)
				return
			}

			out, err = util.ExecCmdInPod(pod, cmdCombinedChannelsCurrent...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			channelCombined, err := strconv.Atoi(strings.TrimSpace(out))
			if err != nil || channelCombined != 1 {
				util.Logf(errUnableToLowerChannels, phyDev, node.Name, err)
				return
			}

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelNetdev))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelNetdev+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating profile %s", profileNetdev))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", profileNetdev)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the number of multi-purpose channels is set to %d for %s on node %s", chLen, phyDev, node.Name))
			err = wait.PollUntilContextTimeout(context.TODO(), pollInterval, waitDuration, true, func(ctx context.Context) (bool, error) {
				out, err = util.ExecCmdInPod(pod, cmdCombinedChannelsCurrent...)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				channelCombined, _ := strconv.Atoi(strings.TrimSpace(out))
				if channelCombined == chLen {
					return true, nil
				}

				return false, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			ginkgo.By(fmt.Sprintf("deleting profile %s", profileNetdev))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileNetdev)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelNetdev, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelNetdev+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the number of multi-purpose channels was rolled back to 1 for %s on node %s", phyDev, node.Name))
			err = wait.PollUntilContextTimeout(context.TODO(), pollInterval, waitDuration, true, func(ctx context.Context) (bool, error) {
				out, err = util.ExecCmdInPod(pod, cmdCombinedChannelsCurrent...)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				channelCombined, _ := strconv.Atoi(strings.TrimSpace(out))
				if channelCombined == 1 {
					return true, nil
				}

				return false, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			// Set the original value of multi-purpose channels.  Ignore failures.
			_, _ = util.ExecCmdInPod(pod, "bash", "-c", fmt.Sprintf("ethtool -L %s combined %d", phyDev, channelOrigCombined))
		})
	})
})
