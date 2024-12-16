package e2e

import (
	"context"
	"fmt"
	"path/filepath"
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

// Test the functionality of the preStop container lifecycle hook -- TuneD settings rollback.
var _ = ginkgo.Describe("[basic][rollback] Node Tuning Operator settings rollback", func() {
	const (
		profileSHMMNI       = "../testing_manifests/deferred/tuned-basic-00.yaml"
		profileIngress      = "../../../examples/ingress.yaml"
		podLabelIngress     = "tuned.openshift.io/ingress"
		sysctlTCPTWReuseVar = "net.ipv4.tcp_tw_reuse"
		sysctlValDef        = "2" // default value of 'sysctlTCPTWReuseVar'
	)

	ginkgo.Context("TuneD settings rollback", func() {
		var (
			profilePath    string
			currentDirPath string
			pod            *coreapi.Pod
		)

		ginkgo.BeforeEach(func() {
			var err error
			currentDirPath, err = util.GetCurrentDirPath()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			profilePath = filepath.Join(currentDirPath, profileIngress)
		})

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			// Ignore failures to cleanup resources which are already deleted or not yet created.
			ginkgo.By("cluster changes rollback")
			if pod != nil {
				_, _, _ = util.ExecAndLogCommand("oc", "label", "pod", "--overwrite", "-n", ntoconfig.WatchNamespace(), pod.Name, podLabelIngress+"-")
			}
			_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profilePath)
		})

		ginkgo.It(fmt.Sprintf("%s set", sysctlTCPTWReuseVar), func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)
			var explain string

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node := &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err = util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Expect the default worker node profile applied prior to getting any current values.
			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", util.GetDefaultWorkerProfile(node), node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, util.GetDefaultWorkerProfile(node), tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the default %s value (%s) is set in Pod %s", sysctlTCPTWReuseVar, sysctlValDef, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlTCPTWReuseVar, sysctlValDef)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling Pod %s with label %s", pod.Name, podLabelIngress))
			_, _, err = util.ExecAndLogCommand("oc", "label", "pod", "--overwrite", "-n", ntoconfig.WatchNamespace(), pod.Name, podLabelIngress+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating custom profile %s", profilePath))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", profilePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("ensuring the custom worker node profile was set")
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlTCPTWReuseVar, "1")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting Pod %s", pod.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "pod", pod.Name, "--wait")
			pod = nil
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for a new TuneD Pod to be ready on node %s", node.Name))
			err = wait.PollUntilContextTimeout(context.TODO(), pollInterval, waitDuration, true, func(ctx context.Context) (bool, error) {
				pod, err = util.GetTunedForNode(cs, node)
				if err != nil {
					explain = err.Error()
					return false, nil
				}
				return true, nil
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)

			// rollback = not_on_exit in tuned-main.conf file prevents settings rollback at TuneD exit
			ginkgo.By(fmt.Sprintf("ensuring the custom %s value (%s) is still set in Pod %s", sysctlTCPTWReuseVar, "1", pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlTCPTWReuseVar, "1")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting custom profile %s", profilePath))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profilePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", util.GetDefaultWorkerProfile(node), node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, util.GetDefaultWorkerProfile(node), tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			_, err = util.ExecCmdInPod(pod, "sysctl", fmt.Sprintf("%s=%s", sysctlTCPTWReuseVar, sysctlValDef))
			gomega.Expect(err).NotTo(gomega.HaveOccurred()) // sysctl exits 1 when it fails to configure a kernel parameter at runtime

			ginkgo.By(fmt.Sprintf("ensuring the default %s value (%s) is set in Pod %s", sysctlTCPTWReuseVar, sysctlValDef, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlTCPTWReuseVar, sysctlValDef)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})

	ginkgo.Context("TuneD settings rollback without pod restart", func() {
		var (
			profilePath    string
			currentDirPath string
		)

		ginkgo.BeforeEach(func() {
			var err error
			currentDirPath, err = util.GetCurrentDirPath()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			profilePath = filepath.Join(currentDirPath, profileSHMMNI)
		})

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			// Ignore failures to cleanup resources which are already deleted or not yet created.
			ginkgo.By("cluster changes rollback")
			_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profilePath)
		})

		ginkgo.It("kernel.shmmni set", func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)

			tuned, err := util.LoadTuned(profilePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node := &nodes[0]
			defaultProfileName := util.GetDefaultWorkerProfile(node)

			// Expect the default worker node profile applied prior to getting any current values.
			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", defaultProfileName, node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, defaultProfileName, tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("checking the pristine state on node %s", node.Name))
			// before the test profile is applied, current node state matches the pristine node state
			verifData := util.MustExtractVerificationOutputAndCommand(cs, node, tuned)
			gomega.Expect(verifData.OutputCurrent).ToNot(gomega.Equal(verifData.OutputExpected), "current pristine output %q already matches expected %q", verifData.OutputCurrent, verifData.OutputExpected)

			ginkgo.By(fmt.Sprintf("creating custom profile %s", profilePath))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", profilePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", "test-shmmni", node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, "test-shmmni", tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("ensuring the custom worker node profile was set")
			out, err := util.ExecCmdInPod(verifData.TargetTunedPod, verifData.CommandArgs...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			out = strings.TrimSpace(out)
			gomega.Expect(out).To(gomega.Equal(verifData.OutputExpected), "command %q output %q does not match desired %q", verifData.CommandArgs, out, verifData.OutputExpected)

			ginkgo.By(fmt.Sprintf("deleting custom profile %s", profilePath))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profilePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", defaultProfileName, node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, defaultProfileName, tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the pristine state is restored on node %s", node.Name))
			out, err = util.ExecCmdInPod(verifData.TargetTunedPod, verifData.CommandArgs...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			out = strings.TrimSpace(out)
			gomega.Expect(out).To(gomega.Equal(verifData.OutputCurrent), "command %q output %q does not match pristine %q", verifData.CommandArgs, out, verifData.OutputCurrent)
		})
	})
})
