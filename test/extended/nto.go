package extended

import (
	"strings"

	utils "github.com/openshift/cluster-node-tuning-operator/test/extended/utils"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
)

var _ = g.Describe("[sig-tuning-node] PSAP should", g.Label("conformance"), func() {
	defer g.GinkgoRecover()

	var (
		oc            = utils.NewCLIWithoutNamespace("nto-test")
		ntoNamespace  = "openshift-cluster-node-tuning-operator"
		ntoFixture    = func(name string) string { return utils.FixturePath("nto", name) }
		ntoIRQSMPFile = ntoFixture("default-irq-smp-affinity.yaml")

		isNTO         bool
		iaasPlatform  string
		tunedNodeName string
		err           error
	)

	g.BeforeEach(func() {
		// ensure NTO operator is installed
		isNTO = utils.IsNTOPodInstalled(oc, ntoNamespace)
		// get IaaS platform
		platformOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platform}").Output()
		if err == nil {
			iaasPlatform = strings.ToLower(platformOutput)
		}
		utils.Logf("Cloud provider is: %v", iaasPlatform)
	})

	g.It("[Jira:NTO]OCP-37415-Allow setting isolated_cores without touching the default_irq_affinity[OTP][Disruptive][Suite:openshift/conformance/serial]", g.Label("Serial"), func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		isSNO := utils.IsSNOCluster(oc)
		// Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = utils.ChoseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = utils.GetFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		defer func() {
			if err := oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/default-irq-smp-affinity-").Execute(); err != nil {
				utils.Logf("Warning: failed to remove label: %v", err)
			}
		}()

		g.By("label the node with default-irq-smp-affinity ")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/default-irq-smp-affinity=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the default values of /proc/irq/default_smp_affinity on worker nodes")

		// This test case must got the value of default_smp_affinity without warning information
		defaultSMPAffinity, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "cat", "/proc/irq/default_smp_affinity").Output()
		utils.Logf("the default value of /proc/irq/default_smp_affinity without cpu affinity is: %v", defaultSMPAffinity)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultSMPAffinity).NotTo(o.BeEmpty())
		defaultSMPAffinity = strings.ReplaceAll(defaultSMPAffinity, ",", "")
		defaultSMPAffinityMask := utils.GetDefaultSMPAffinityBitMaskbyCPUCores(oc, tunedNodeName)
		o.Expect(defaultSMPAffinity).To(o.ContainSubstring(defaultSMPAffinityMask))

		utils.Logf("the value of /proc/irq/default_smp_affinity: %v", defaultSMPAffinityMask)
		cpuBitsMask := utils.ConvertCPUBitMaskToByte(defaultSMPAffinityMask)
		o.Expect(cpuBitsMask).NotTo(o.BeEmpty())

		ntoRes1 := utils.NtoResource{
			Name:        "default-irq-smp-affinity",
			Namespace:   ntoNamespace,
			Template:    ntoIRQSMPFile,
			Sysctlparm:  "#default_irq_smp_affinity",
			Sysctlvalue: "1",
		}

		defer ntoRes1.Delete(oc)

		g.By("create default-irq-smp-affinity profile to enable isolated_cores=1")
		ntoRes1.CreateIRQSMPAffinityProfileIfNotExist(oc)

		g.By("check if new NTO profile was applied")
		ntoRes1.AssertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "default-irq-smp-affinity", "True")

		g.By("check values of /proc/irq/default_smp_affinity on worker nodes after enabling isolated_cores=1")
		isolatedcoresSMPAffinity, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "cat", "/proc/irq/default_smp_affinity").Output()
		isolatedcoresSMPAffinity = strings.ReplaceAll(isolatedcoresSMPAffinity, ",", "")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isolatedcoresSMPAffinity).NotTo(o.BeEmpty())
		utils.Logf("the value of default_smp_affinity after setting isolated_cores=1 is: %v", isolatedcoresSMPAffinity)

		g.By("verify if the value of /proc/irq/default_smp_affinity is affected by isolated_cores=1")
		// Isolate the second cpu cores, the default_smp_affinity should be changed
		isolatedCPU := utils.ConvertIsolatedCPURange2CPUList("1")
		o.Expect(isolatedCPU).NotTo(o.BeEmpty())

		newSMPAffinityMask := utils.AssertIsolateCPUCoresAffectedBitMask(cpuBitsMask, isolatedCPU)
		o.Expect(newSMPAffinityMask).NotTo(o.BeEmpty())
		o.Expect(isolatedcoresSMPAffinity).To(o.ContainSubstring(newSMPAffinityMask))

		g.By("remove the old profile and create a new one later ...")
		ntoRes1.Delete(oc)

		ntoRes2 := utils.NtoResource{
			Name:        "default-irq-smp-affinity",
			Namespace:   ntoNamespace,
			Template:    ntoIRQSMPFile,
			Sysctlparm:  "default_irq_smp_affinity",
			Sysctlvalue: "1",
		}

		defer ntoRes2.Delete(oc)
		g.By("create default-irq-smp-affinity profile to enable default_irq_smp_affinity=1")
		ntoRes2.CreateIRQSMPAffinityProfileIfNotExist(oc)

		g.By("check if new NTO profile was applied")
		ntoRes2.AssertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "default-irq-smp-affinity", "True")

		g.By("check values of /proc/irq/default_smp_affinity on worker nodes")
		// We only need to return the value /proc/irq/default_smp_affinity without stdErr
		IRQSMPAffinity, _, err := utils.DebugNodeRetryWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"--quiet=true", "--to-namespace=" + ntoNamespace}, "cat", "/proc/irq/default_smp_affinity")
		IRQSMPAffinity = strings.ReplaceAll(IRQSMPAffinity, ",", "")
		o.Expect(IRQSMPAffinity).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		// Isolate the second cpu cores, the default_smp_affinity should be changed
		utils.Logf("the value of default_smp_affinity after setting default_irq_smp_affinity=1 is: %v", IRQSMPAffinity)
		isMatch := utils.AssertDefaultIRQSMPAffinityAffectedBitMask(cpuBitsMask, isolatedCPU, IRQSMPAffinity)
		o.Expect(isMatch).To(o.Equal(true))
	})

})
