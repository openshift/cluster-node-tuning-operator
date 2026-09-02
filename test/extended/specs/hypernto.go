package specs

import (
	"context"
	"fmt"
	"os"
	"strings"

	utils "github.com/openshift/cluster-node-tuning-operator/test/extended/utils"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	oteg "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
)

var _ = g.Describe("[Jira:Node Tuning Operator][sig-tuning-node] should", g.Label("conformance"), func() {
	defer g.GinkgoRecover()

	const (
		ntoNamespace = "openshift-cluster-node-tuning-operator"
	)

	var (
		oc           = utils.NewCLIWithoutNamespace()
		iaasPlatform string
		fx           *ntoFx
	)

	g.BeforeEach(func() {
		var err error
		fx = &ntoFx{}
		fx.baseDir, err = os.MkdirTemp("", "cluster-node-tuning-operator-test-ext-")
		if err != nil {
			g.Fail(fmt.Sprintf("failed to create fixtures temp dir: %v", err))
		}

		// ensure NTO operator is installed
		SkipNoNTO(oc, ntoNamespace)

		// get IaaS platform
		platformOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platform}").Output()
		if err == nil {
			iaasPlatform = strings.ToLower(platformOutput)
		}
		utils.Logf("cloud provider is: %v", iaasPlatform)
	})

	g.AfterEach(func() {
		if fx != nil && fx.baseDir != "" {
			if err := os.RemoveAll(fx.baseDir); err != nil {
				utils.Logf("warning: failed to remove fixture temp dir %s: %v", fx.baseDir, err)
			}
		}
	})

	// Consider dropping this test case.  It mostly duplicates HyperShift tests and
	// setting "vm.dirty_ratio" via sysctl is deprecated now using tuned; [vm] dirty_bytes with % should be used instead.
	// author: liqcui@redhat.com
	g.It("[test_id:63223][OTP]tune sysctl and kernel parameters for all nodes in a HyperShift nodepool [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		// This is a ROSA HCP pre-defined case, only check result, ROSA team will create NTO tuned profile when ROSA HCP created, remove Disruptive
		// Only execute on ROSA hosted cluster
		isROSA := utils.IsROSAHostedCluster(oc)
		if !isROSA {
			g.Skip("It's not ROSA hosted cluster - skipping test")
		}

		// For ROSA Environment, we are unable to access management cluster, so discussed with ROSA team,
		// ROSA team create pre-defined configmap and applied to specified nodepool with hardcode profile name.
		// NTO will only check if all setting applied to the worker node on hosted cluster.
		g.By("check if the tuned hc-nodepool-vmdratio is created in hosted cluster nodepool")
		tunedNameList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNameList).NotTo(o.BeEmpty())
		utils.Logf("The list of tuned profiles is: \n%v", tunedNameList)
		o.Expect(tunedNameList).To(o.And(o.ContainSubstring("hc-nodepool-vmdratio"),
			o.ContainSubstring("tuned-hugepages")))

		appliedProfileList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(appliedProfileList).NotTo(o.BeEmpty())
		o.Expect(appliedProfileList).To(o.And(o.ContainSubstring("hc-nodepool-vmdratio"),
			o.ContainSubstring("openshift-node-hugepages")))

		g.By("get the node name that applied to the profile hc-nodepool-vmdratio")
		tunedNodeNameOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace, `-ojsonpath={.items[?(@..status.tunedProfile=="hc-nodepool-vmdratio")].metadata.name}`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNodeNameOutput).NotTo(o.BeEmpty())
		vmDirtyRatioNodeNames := strings.Fields(tunedNodeNameOutput)
		o.Expect(vmDirtyRatioNodeNames).NotTo(o.BeEmpty())

		g.By("assert the value of sysctl vm.dirty_ratio, the expected value should be 55")
		for _, tunedNodeName := range vmDirtyRatioNodeNames {
			debugNodeStdout, err := utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "sysctl", "vm.dirty_ratio")
			o.Expect(err).NotTo(o.HaveOccurred())
			utils.Logf("the value of sysctl vm.dirty_ratio on node %v is: \n%v\n", tunedNodeName, debugNodeStdout)
			o.Expect(debugNodeStdout).To(o.ContainSubstring("vm.dirty_ratio = 55"))
		}

		g.By("get the node name that applied to the profile openshift-node-hugepages")
		tunedNodeNameOutput, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace, `-ojsonpath={.items[?(@..status.tunedProfile=="openshift-node-hugepages")].metadata.name}`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNodeNameOutput).NotTo(o.BeEmpty())
		hugepagesNodeNames := strings.Fields(tunedNodeNameOutput)
		o.Expect(hugepagesNodeNames).NotTo(o.BeEmpty())

		g.By("assert the value of cat /proc/cmdline, the expected value should be hugepagesz=2M hugepages=50")
		for _, tunedNodeName := range hugepagesNodeNames {
			debugNodeStdout, err := utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "cat", "/proc/cmdline")
			o.Expect(err).NotTo(o.HaveOccurred())
			utils.Logf("the value of /proc/cmdline on node %v is: \n%v\n", tunedNodeName, debugNodeStdout)
			o.Expect(debugNodeStdout).To(o.ContainSubstring("hugepagesz=2M hugepages=50"))
		}
	})
})
