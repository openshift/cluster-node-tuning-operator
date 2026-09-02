package specs

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	utils "github.com/openshift/cluster-node-tuning-operator/test/extended/utils"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	oteg "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"k8s.io/apimachinery/pkg/util/wait"
)

// ntoFx manages per-test temporary fixture files.  Each test gets its own temp
// directory, created in BeforeEach and removed in AfterEach.  Fixtures are
// materialized lazily -- only the files a test actually uses are written.
type ntoFx struct {
	baseDir string
}

func (f *ntoFx) file(subdir, name string) string {
	path, err := utils.TestdataFixturePathBase(f.baseDir, subdir, name)
	if err != nil {
		g.Fail(fmt.Sprintf("failed to create fixture file %s/%s: %v", subdir, name, err))
	}
	return path
}

var cloudPlatforms = []string{"aws", "gcp", "azure", "ibmcloud", "alibabacloud"}

var _ = g.Describe("[Jira:Node Tuning Operator][sig-tuning-node] should", g.Label("conformance"), func() {
	defer g.GinkgoRecover()

	const (
		ntoNamespace = "openshift-cluster-node-tuning-operator"
		paoNamespace = "openshift-performance-addon-operator"
		nginxAlpine  = "quay.io/openshifttest/nginx-alpine@sha256:04f316442d48ba60e3ea0b5a67eb89b0b667abf1c198a3d0056ca748736336a0"
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

		// this file should not contain any tests for HyperShift hosted clusters
		SkipIsHyperShiftHostedCluster(oc)

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

	// A dummy test that should always pass.  It should land in "openshift/cluster-node-tuning-operator/conformance/parallel" suite.
	g.It("support passing tests", func() {
		o.Expect(true).To(o.BeTrue())
	})

	// A dummy test that should always pass.  It should land in "openshift/cluster-node-tuning-operator/conformance/serial" suite.
	g.It("support passing tests [Serial]", func() {
		o.Expect(true).To(o.BeTrue())
	})

	// A dummy test that should always pass.  It should land in "openshift/cluster-node-tuning-operator/disruptive" suite.
	g.It("support passing tests [Disruptive]", func() {
		o.Expect(true).To(o.BeTrue())
	})

	// A dummy test that should always pass.  It should land in "openshift/cluster-node-tuning-operator/optional/slow" suite.
	g.It("support passing tests [Slow]", func() {
		o.Expect(true).To(o.BeTrue())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:29789][OTP]preserve sysctl values set via system sysctl *.conf files when reapply_sysctl is true and allow override when false [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		g.By("pick one worker node and one tuned pod on same node")
		workerNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", workerNodeName, "tuned.openshift.io/override-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuneds.tuned.openshift.io", "override").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		utils.Logf("worker Node: %v", workerNodeName)
		tunedPodName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pods", "-n", ntoNamespace, "--field-selector=spec.nodeName="+workerNodeName, "-l", "openshift-app=tuned", "-o=jsonpath={.items[0].metadata.name}").Output()
		o.Expect(tunedPodName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("tuned Pod: %v", tunedPodName)

		g.By("check values set by /etc/sysctl on node and store the values")
		inotify, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "cat", "/etc/sysctl.d/inotify.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(inotify).To(o.And(
			o.ContainSubstring("fs.inotify.max_user_watches"),
			o.ContainSubstring("fs.inotify.max_user_instances")))
		maxUserWatchesValue, err := utils.GetMaxUserWatchesValue(inotify)
		o.Expect(err).NotTo(o.HaveOccurred())
		maxUserInstancesValue, err := utils.GetMaxUserInstancesValue(inotify)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("fs.inotify.max_user_watches has value of: %v", maxUserWatchesValue)
		utils.Logf("fs.inotify.max_user_instances has value of: %v", maxUserInstancesValue)

		g.By("mount /etc/sysctl on node")
		_, err = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", ntoNamespace, tunedPodName, "--", "mount").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check sysctl kernel.pid_max on node and store the value")
		kernel, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "kernel.pid_max")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernel).To(o.ContainSubstring("kernel.pid_max"))
		pidMaxValue, err := utils.GetKernelPidMaxValue(kernel)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("kernel.pid_max has value of: %v", pidMaxValue)

		// tuned can not override parameters set via /etc/sysctl{.conf,.d} when reapply_sysctl=true
		// The settings in /etc/sysctl.d/inotify.conf as below
		//      fs.inotify.max_user_watches = 65536     => Try to override to 163840 by tuned, expect the old value 65536
		//      fs.inotify.max_user_instances = 8192    => Not override by tuned, expect the old value 8192
		//      kernel.pid_max = 4194304                => Default value is 4194304
		// The settings in custom tuned profile as below
		//      fs.inotify.max_user_watches = 163840    => Try to override to 163840 by tuned, expect the old value 65536
		//      kernel.pid_max = 1048576                => Override by tuned, expect the new value 1048576

		g.By("create new NTO CR with reapply_sysctl=true and label the node")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", workerNodeName, "tuned.openshift.io/override=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		overrideYaml := fx.file("nto", "override.yaml")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", overrideYaml, "-p", "REAPPLY_SYSCTL=true")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, workerNodeName, "override")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check value of fs.inotify.max_user_instances on node (set by sysctl, should be the same as before), expected value is 8192")
		maxUserInstanceCheck, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_instances")
		utils.Logf("fs.inotify.max_user_instances has value of: %v", maxUserInstanceCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserInstanceCheck).To(o.ContainSubstring("fs.inotify.max_user_instances = " + maxUserInstancesValue))

		g.By("check value of fs.inotify.max_user_watches on node (set by sysctl, should be the same as before), expected value is 65536")
		maxUserWatchesCheck, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_watches")
		utils.Logf("fs.inotify.max_user_watches has value of: %v", maxUserWatchesCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserWatchesCheck).To(o.ContainSubstring("fs.inotify.max_user_watches = " + maxUserWatchesValue))

		g.By("check value of kernel.pid_max on node (set by override tuned, should be the same value of override custom profile), expected value is 1048576")
		pidMaxCheck, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "kernel.pid_max")
		utils.Logf("kernel.pid_max has value of: %v", pidMaxCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(pidMaxCheck).To(o.ContainSubstring("kernel.pid_max = 1048576"))

		// tuned can override parameters set via /etc/sysctl{.conf,.d} when reapply_sysctl=false
		// The settings in /etc/sysctl.d/inotify.conf as below
		//     fs.inotify.max_user_watches = 65536     => Try to override to 163840 by tuned, expect the old value 163840
		//     fs.inotify.max_user_instances = 8192    => Not override by tuned, expect the old value 8192
		//     kernel.pid_max = 4194304                => Default value is 4194304
		// The settings in custom tuned profile as below
		//     fs.inotify.max_user_watches = 163840    => Try to override to 163840 by tuned, expect the old value 163840
		//     kernel.pid_max = 1048576                => Override by tuned, expect the new value 1048576

		g.By("create new CR with reapply_sysctl=false")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", overrideYaml, "-p", "REAPPLY_SYSCTL=false")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check value of fs.inotify.max_user_instances on node (set by sysctl, should be the same as before), expected value is 8192")
		maxUserInstanceCheck, _, err = utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_instances")
		utils.Logf("fs.inotify.max_user_instances has value of: %v", maxUserInstanceCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserInstanceCheck).To(o.ContainSubstring("fs.inotify.max_user_instances = " + maxUserInstancesValue))

		g.By("check value of fs.inotify.max_user_watches on node (set by sysctl, should be the same value of override custom profile), expected value is 163840")
		maxUserWatchesCheck, _, err = utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_watches")
		utils.Logf("fs.inotify.max_user_watches has value of: %v", maxUserWatchesCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserWatchesCheck).To(o.ContainSubstring("fs.inotify.max_user_watches = 163840"))

		g.By("check value of kernel.pid_max on node (set by override tuned, should be the same value of override custom profile), expected value is 1048576")
		pidMaxCheck, _, err = utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "kernel.pid_max")
		utils.Logf("kernel.pid_max has value of: %v", pidMaxCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(pidMaxCheck).To(o.ContainSubstring("kernel.pid_max = 1048576"))
	})

	// author: nweinber@redhat.com
	g.It("[test_id:33237][OTP]support operatorapi Managed and Unmanaged states [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		var profileCheck string

		masterNodeName, err := utils.GetFirstMasterNodeName(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		defaultMasterProfileName, err := utils.GetDefaultProfileNameOnMaster(oc, masterNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch-").Execute()
			_ = utils.PatchTunedState(oc, ntoNamespace, "default", "Managed")
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuned", "nf-conntrack-max", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		isSNOOrCompact := utils.IsSNOOrCompact(oc)

		g.By("patch default tuned to 'Unmanaged'")
		err = utils.PatchTunedState(oc, ntoNamespace, "default", "Unmanaged")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err := utils.GetTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Unmanaged"))

		g.By("label the node with tuned.openshift.io/elasticsearch=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("tuned Pod: %v", tunedPodName)

		g.By("create new profile from CR")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "tuned-nf-conntrack-max-node.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("current profile on each node:")
		stdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("profile Name Per Nodes: %v", stdOut)

		if isSNOOrCompact {
			profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal(defaultMasterProfileName))
		} else {
			profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal("openshift-node"))
		}

		nodeList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		nodes := strings.Fields(nodeList)

		for _, node := range nodes {
			output, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, node, nil, "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
		}

		g.By("remove custom profile and patch default tuned back to Managed")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuned", "nf-conntrack-max").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.PatchTunedState(oc, ntoNamespace, "default", "Managed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = utils.GetTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Managed"))

		g.By("label the node with tuned.openshift.io/elasticsearch=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedPodName, err = utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("tuned Pod: %v", tunedPodName)

		g.By("create new profile from CR")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "tuned-nf-conntrack-max-node.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("current profile on each node:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("profile Name Per Nodes: %v", stdOut)

		g.By("assert nf-conntrack-max applied to the labeled node")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "nf-conntrack-max")
		o.Expect(err).NotTo(o.HaveOccurred())

		profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("nf-conntrack-max"))

		g.By("current profile on each node:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("profile Name Per Nodes: %v", stdOut)

		// tuned nodes should have value of 1048578, others should be 1048576
		for _, node := range nodes {
			output, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, node, nil, "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			if node == tunedNodeName {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048578"))
			} else {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
			}
		}

		g.By("change tuned state back to Unmanaged and delete custom tuned")
		err = utils.PatchTunedState(oc, ntoNamespace, "default", "Unmanaged")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = utils.GetTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Unmanaged"))
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuned", "nf-conntrack-max").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("nf-conntrack-max"))

		g.By("assert the log contains recommended profile (nf-conntrack-max) matches current configuration")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "20", 180, `'nf-conntrack-max' applied|recommended profile \(nf-conntrack-max\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("current profile on each node:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("profile Name Per Nodes: %v", stdOut)

		// tuned nodes should have value of 1048578, others should be 1048576
		for _, node := range nodes {
			output, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, node, nil, "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			if node == tunedNodeName {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048578"))
			} else {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
			}
		}

		g.By("change tuned state back to Managed")
		err = utils.PatchTunedState(oc, ntoNamespace, "default", "Managed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = utils.GetTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Managed"))

		if isSNOOrCompact {
			err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, defaultMasterProfileName)
			o.Expect(err).NotTo(o.HaveOccurred())

			profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal(defaultMasterProfileName))
		} else {
			err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node")
			o.Expect(err).NotTo(o.HaveOccurred())

			profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal("openshift-node"))
		}

		g.By("current profile on each node:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("profile Name Per Nodes: %v", stdOut)

		for _, node := range nodes {
			output, _, err := utils.DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, node, nil, "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
		}
	})

	// author: liqcui@redhat.com
	// [Timeout:30m] applied because this test often exceeds the 15-minute default.
	g.It("[test_id:36881][OTP]provide machine config for the master machine config pool when a performance profile is applied [Disruptive][Slow][Timeout:30m]", oteg.Informing(), func(ctx context.Context) {
		isSingleMaster, err := utils.IsSingleMasterCluster(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		if isSingleMaster {
			g.Skip("Cluster with a single master - skipping test")
		}

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuneds.tuned.openshift.io", "openshift-node-performance-hp-performanceprofile", "--ignore-not-found").Execute()
			_ = utils.WaitForMCPUpdate(cleanupCtx, oc, "master", 1800)
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("add new tuning profile from CR")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "hp-performanceprofile.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("verify new tuned profile was created")
		profiles, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profiles).To(o.ContainSubstring("openshift-node-performance-hp-performanceprofile"))

		g.By("get NTO pod name and check logs for priority warning")
		ntoPodName, err := utils.GetNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("NTO pod name: %v", ntoPodName)
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, ntoPodName, "10", 180, `openshift-node-performance-hp-performanceprofile have the same priority 30.*please use a different priority for your custom profiles`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("patch priority for openshift-node-performance-hp-performanceprofile tuned to 18")
		err = utils.PatchTunedProfile(oc, ntoNamespace, "openshift-node-performance-hp-performanceprofile", fx.file("nto", "hp-performanceprofile-patch.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())
		tunedPriority, err := utils.GetTunedPriority(oc, ntoNamespace, "openshift-node-performance-hp-performanceprofile")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPriority).To(o.Equal("18"))

		g.By("check MachineConfig for expected changes")
		// NTO must generate the master MachineConfig for the master pool with
		// the kernel arguments derived from the applied profile.
		err = utils.WaitForMachineConfigWithKernelArg(ctx, oc, "50-nto-master", "default_hugepagesz", "2M", 300)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check MachineConfigPool for expected changes")
		// Applying the master MachineConfig makes MCO reboot all master nodes
		// sequentially, so the pool does not fully converge until the last
		// master reboot is done, which routinely outlives the per-spec
		// timeout.  Verify only that the pool picked up the new configuration;
		// the full pool convergence is awaited in the cleanup.
		err = utils.WaitForMCPUpdateStarted(ctx, oc, "master", 300)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check Nodes for expected changes")
		masterNodeName, err := utils.WaitForSchedulingDisabledNode(ctx, oc, "node-role.kubernetes.io/control-plane=")
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("the master node %v is being rebooted", masterNodeName)

		g.By("ensure the settings took effect on the master nodes, only check the first rebooted node")
		err = utils.WaitForMasterNodeChanges(ctx, oc, masterNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:43173][OTP]affine blacklisted cgroup processes to the default cpuset [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get how many cpus on the specified worker node
		g.By("get the number of CPU cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node does not have enough cpus - skipping test")
		}

		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("remove custom profile (if not already removed) and remove node label")
		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "-n", ntoNamespace, "cgroup-scheduler-affinecpuset").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node-").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the specified linux node with label tuned-scheduler-node")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// setting cgroup_ps_blacklist=/kubepods\.slice/
		// the process belonging to /kubepods\.slice/ can consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		// the process not belonging to /kubepods\.slice/ cannot consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0 or 0,2-N

		g.By("create NTO custom tuned profile cgroup-scheduler-affinecpuset")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "cgroup-scheduler-blacklist.yaml"), "-p", "PROFILE_NAME=cgroup-scheduler-affinecpuset", `CGROUP_BLACKLIST=/kubepods\.slice/`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if NTO custom tuned profile cgroup-scheduler-affinecpuset was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "cgroup-scheduler-affinecpuset")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		g.By("verify the cpu allow list in cgroup black list for tuned")
		result, err := utils.AssertProcessInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "tuned", nodeCPUCoresInt)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.Equal(true))

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0 or 0,2-N
		g.By("verify the cpu allow list in cgroup black list for chronyd")
		result, err = utils.AssertProcessExcludedFromCgroupScheduler(oc, tunedNodeName, ntoNamespace, "chronyd", nodeCPUCoresInt)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.Equal(true))
	})

	g.It("[test_id:27491][OTP]support custom profiles via node label matching functionality [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		ntoRes := utils.NtoResource{
			Name:        "user-max-mnt-namespaces",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "custom-tuned-profiles-node.yaml"),
			SysctlParam: "user.max_mnt_namespaces",
			SysctlValue: "142214",
		}

		masterNodeName, err := utils.GetFirstMasterNodeName(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		defaultMasterProfileName, err := utils.GetDefaultProfileNameOnMaster(oc, masterNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch-").Execute()
			_ = ntoRes.Delete(oc)
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		isSNOOrCompact := utils.IsSNOOrCompact(oc)

		g.By("label the node with tuned.openshift.io/elasticsearch=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Apply new profile that match label tuned.openshift.io/elasticsearch=
		g.By("create new profile from CR")
		err = ntoRes.CreateCustomTunedProfile(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new profile user-max-mnt-namespaces applied to labeled node")
		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "user-max-mnt-namespaces")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("user-max-mnt-namespaces"))

		g.By("assert 'user-max-mnt-namespaces' applied in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "10", 180, `'user-max-mnt-namespaces' applied|recommended profile \(user-max-mnt-namespaces\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("compare the value user.max_mnt_namespaces on node with labeled node, should be 142214")
		err = utils.CompareSysctlValueOnAllWorkerNodesWithRetry(ctx, oc, tunedNodeName, "user.max_mnt_namespaces", "", "142214")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("delete custom tuned profile user.max_mnt_namespaces")
		err = ntoRes.Delete(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Check if restore to default profile.
		if isSNOOrCompact {
			g.By("the cluster is SNO or Compact Cluster")
			err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, defaultMasterProfileName)
			o.Expect(err).NotTo(o.HaveOccurred())
			g.By("assert default profile applied in tuned pod log")
			err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "10", 180, "'"+defaultMasterProfileName+"' applied|recommended profile \\("+defaultMasterProfileName+"\\) matches current configuration")
			o.Expect(err).NotTo(o.HaveOccurred())
			profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal(defaultMasterProfileName))
		} else {
			g.By("the cluster is regular OCP Cluster")
			err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node")
			o.Expect(err).NotTo(o.HaveOccurred())
			g.By("assert profile 'openshift-node' applied in tuned pod log")
			err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "10", 180, `'openshift-node' applied|recommended profile \(openshift-node\) matches current configuration`)
			o.Expect(err).NotTo(o.HaveOccurred())
			profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal("openshift-node"))
		}

		g.By("check all nodes for user.max_mnt_namespaces value, all nodes should be different from 142214")
		err = utils.CompareSysctlDifferentFromSpecifiedValueByNameWithRetry(ctx, oc, "user.max_mnt_namespaces", "142214")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	g.It("[test_id:37125][OTP]enable and disable debug logging for tuned containers [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		ntoRes := utils.NtoResource{
			Name:        "user-max-net-namespaces",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "nto-tuned-debug-node.yaml"),
			SysctlParam: "user.max_net_namespaces",
			SysctlValue: "101010",
		}

		var (
			isEnableDebug bool
			isDebugInLog  bool
			tunedPodName  string
		)

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch-").Execute()
			_ = ntoRes.Delete(oc)
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with tuned.openshift.io/elasticsearch=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedPodName, err = utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Delete tuned pod to reset logs for re-entrancy (run safety)
		oldPodName := tunedPodName
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", oldPodName, "-n", ntoNamespace, "--ignore-not-found=true").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		pollCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		err = wait.PollUntilContextTimeout(pollCtx, 2*time.Second, 2*time.Minute, false, func(_ context.Context) (bool, error) {
			newPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
			if err != nil {
				utils.Logf("failed to get tuned pod name by node %s: %v", tunedNodeName, err)
				return false, nil
			}
			if newPodName != "" && newPodName != oldPodName {
				tunedPodName = newPodName
				return true, nil
			}
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.AssertPodToBeReady(ctx, oc, tunedPodName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Verify if debug was disabled by default
		g.By("check node profile debug settings, it should be debug: false")
		isEnableDebug, err = utils.AssertDebugSettings(oc, tunedNodeName, ntoNamespace, "false")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isEnableDebug).To(o.Equal(true))

		// Apply new profile that match label tuned.openshift.io/elasticsearch=
		g.By("create new profile from CR with debug setting is false")
		err = ntoRes.CreateDebugTunedProfile(oc, false)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "user-max-net-namespaces", "True")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("user-max-net-namespaces"))

		// Verify nto tuned logs
		g.By("check NTO tuned pod logs to confirm if user-max-net-namespaces applied")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "10", 180, `'user-max-net-namespaces' applied|recommended profile \(user-max-net-namespaces\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())
		// Verify if debug is false by CR setting
		g.By("check node profile debug settings, it should be debug: false")
		isEnableDebug, err = utils.AssertDebugSettings(oc, tunedNodeName, ntoNamespace, "false")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isEnableDebug).To(o.Equal(true))

		// Check if the log contains debug, the expected result should be none
		g.By("check if tuned pod log contains debug key word, the expected result should be no DEBUG")
		isDebugInLog, err = utils.AssertPodLogsContain(oc, tunedPodName, ntoNamespace, "DEBUG")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isDebugInLog).To(o.Equal(false))

		g.By("delete custom profile and will apply a new one")
		err = ntoRes.Delete(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create new profile from CR with debug setting is true")
		debugLogFound := utils.StreamPodLogsForKeyword(oc, tunedPodName, ntoNamespace, "DEBUG", 180)
		g.DeferCleanup(func() {
			select {
			case <-debugLogFound:
			default:
			}
		})
		err = ntoRes.CreateDebugTunedProfile(oc, true)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "user-max-net-namespaces", "True")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("user-max-net-namespaces"))

		// Verify if debug was enabled by CR setting
		g.By("check if the debug is true in node profile, the expected result should be true")
		isEnableDebug, err = utils.AssertDebugSettings(oc, tunedNodeName, ntoNamespace, "true")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isEnableDebug).To(o.Equal(true))

		// The log should contain 'DEBUG' keyword in the pod logs
		g.By(fmt.Sprintf("check if tuned pod '%s' log contains debug key word, the log should contain DEBUG", tunedPodName))
		found := <-debugLogFound
		o.Expect(found).To(o.BeTrue())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:37415][OTP]set isolated_cores without affecting default_irq_smp_affinity [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		ntoRes1 := utils.NtoResource{
			Name:        "default-irq-smp-affinity",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "default-irq-smp-affinity.yaml"),
			SysctlParam: "#default_irq_smp_affinity",
			SysctlValue: "1",
		}

		ntoRes2 := utils.NtoResource{
			Name:        "default-irq-smp-affinity",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "default-irq-smp-affinity.yaml"),
			SysctlParam: "default_irq_smp_affinity",
			SysctlValue: "1",
		}

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/default-irq-smp-affinity-").Execute()
			_ = ntoRes1.Delete(oc)
			_ = ntoRes2.Delete(oc)
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with default-irq-smp-affinity ")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/default-irq-smp-affinity=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the default values of /proc/irq/default_smp_affinity on worker nodes")

		// This test case must got the value of default_smp_affinity without warning information
		defaultSMPAffinity, err := utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "cat", "/proc/irq/default_smp_affinity")
		utils.Logf("the default value of /proc/irq/default_smp_affinity without cpu affinity is: %v", defaultSMPAffinity)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultSMPAffinity).NotTo(o.BeEmpty())
		defaultSMPAffinity = strings.ReplaceAll(defaultSMPAffinity, ",", "")
		defaultSMPAffinityMask, err := utils.GetDefaultSMPAffinityBitMaskByCPUCores(oc, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultSMPAffinity).To(o.ContainSubstring(defaultSMPAffinityMask))

		utils.Logf("the value of /proc/irq/default_smp_affinity: %v", defaultSMPAffinityMask)
		cpuBitsMask, err := utils.ConvertCPUBitMaskToByte(defaultSMPAffinityMask)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuBitsMask).NotTo(o.BeEmpty())

		g.By("create default-irq-smp-affinity profile to enable isolated_cores=1")
		err = ntoRes1.CreateIRQSMPAffinityProfile(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "default-irq-smp-affinity", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check values of /proc/irq/default_smp_affinity on worker nodes after enabling isolated_cores=1")
		isolatedcoresSMPAffinity, err := utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "cat", "/proc/irq/default_smp_affinity")
		isolatedcoresSMPAffinity = strings.ReplaceAll(isolatedcoresSMPAffinity, ",", "")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isolatedcoresSMPAffinity).NotTo(o.BeEmpty())
		utils.Logf("the value of default_smp_affinity after setting isolated_cores=1 is: %v", isolatedcoresSMPAffinity)

		g.By("verify if the value of /proc/irq/default_smp_affinity is affected by isolated_cores=1")
		// Isolate the second cpu cores, the default_smp_affinity should be changed
		isolatedCPU, err := utils.ConvertIsolatedCPURange2CPUList("1")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isolatedCPU).NotTo(o.BeEmpty())

		isMatch, err := utils.AssertIsolateCPUCoresAffectedBitMask(cpuBitsMask, isolatedCPU, isolatedcoresSMPAffinity)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isMatch).To(o.Equal(true))

		g.By("remove the old profile and create a new one later")
		err = ntoRes1.Delete(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create default-irq-smp-affinity profile to enable default_irq_smp_affinity=1")
		err = ntoRes2.CreateIRQSMPAffinityProfile(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "default-irq-smp-affinity", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check values of /proc/irq/default_smp_affinity on worker nodes")
		// We only need to return the value /proc/irq/default_smp_affinity without stdErr
		IRQSMPAffinity, _, err := utils.DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"--quiet=true", "--to-namespace=" + ntoNamespace}, "cat", "/proc/irq/default_smp_affinity")
		IRQSMPAffinity = strings.ReplaceAll(IRQSMPAffinity, ",", "")
		o.Expect(IRQSMPAffinity).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		// Isolate the second cpu cores, the default_smp_affinity should be changed
		utils.Logf("the value of default_smp_affinity after setting default_irq_smp_affinity=1 is: %v", IRQSMPAffinity)
		isMatch, err = utils.AssertDefaultIRQSMPAffinityAffectedBitMask(cpuBitsMask, isolatedCPU, IRQSMPAffinity)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isMatch).To(o.Equal(true))
	})

	// This test can run in Parallel with other tests and is not Disruptive.
	// author: liqcui@redhat.com
	g.It("[test_id:44650][OTP]provide default tuned profiles with correct settings for openshift, openshift-control-plane, and openshift-node", func(ctx context.Context) {
		// Get the tuned pod name that run on first worker node
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check kernel version of worker nodes")
		kernelVersion, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.kernelVersion}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernelVersion).NotTo(o.BeEmpty())

		g.By("check default tuned profile list, should contain openshift-control-plane and openshift-node")
		defaultTunedOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned", "default", "-ojsonpath={.spec.recommend}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedOutput).NotTo(o.BeEmpty())
		o.Expect(defaultTunedOutput).To(o.And(
			o.ContainSubstring("openshift-control-plane"),
			o.ContainSubstring("openshift-node")))

		g.By("check content of tuned file /usr/lib/tuned/openshift/tuned.conf to match default NTO settings")
		openshiftTunedConf, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftTunedConf).NotTo(o.BeEmpty())
		if strings.Contains(kernelVersion, "el8") || strings.Contains(kernelVersion, "el7") {
			o.Expect(openshiftTunedConf).To(o.And(
				o.ContainSubstring("avc_cache_threshold=8192"),
				o.ContainSubstring("kernel.pid_max=>4194304"),
				o.ContainSubstring("net.netfilter.nf_conntrack_max=1048576"),
				o.ContainSubstring("net.ipv4.conf.all.arp_announce=2"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("vm.max_map_count=262144"),
				o.ContainSubstring("/sys/module/nvme_core/parameters/io_timeout=4294967295"),
				o.ContainSubstring(`cgroup_ps_blacklist=/kubepods\.slice/`),
				o.ContainSubstring("runtime=0")))
		} else {
			o.Expect(openshiftTunedConf).To(o.And(
				o.ContainSubstring("avc_cache_threshold=8192"),
				o.ContainSubstring("nf_conntrack_hashsize=1048576"),
				o.ContainSubstring("kernel.pid_max=>4194304"),
				o.ContainSubstring("fs.aio-max-nr=>1048576"),
				o.ContainSubstring("net.netfilter.nf_conntrack_max=1048576"),
				o.ContainSubstring("net.ipv4.conf.all.arp_announce=2"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("vm.max_map_count=262144"),
				o.ContainSubstring("/sys/module/nvme_core/parameters/io_timeout=4294967295"),
				o.ContainSubstring(`cgroup_ps_blacklist=/kubepods\.slice/`),
				o.ContainSubstring("runtime=0")))
		}

		g.By("check content of tuned file /usr/lib/tuned/openshift-control-plane/tuned.conf to match default NTO settings")
		openshiftControlPlaneTunedConf, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift-control-plane/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftControlPlaneTunedConf).NotTo(o.BeEmpty())
		o.Expect(openshiftControlPlaneTunedConf).To(o.ContainSubstring("include=openshift"))

		if strings.Contains(kernelVersion, "el8") || strings.Contains(kernelVersion, "el7") {
			o.Expect(openshiftControlPlaneTunedConf).To(o.And(
				o.ContainSubstring("sched_wakeup_granularity_ns=4000000"),
				o.ContainSubstring("sched_migration_cost_ns=5000000")))
		} else {
			o.Expect(openshiftControlPlaneTunedConf).NotTo(o.ContainSubstring("sched_wakeup_granularity_ns=4000000"))
			o.Expect(openshiftControlPlaneTunedConf).NotTo(o.ContainSubstring("sched_migration_cost_ns=5000000"))
		}

		g.By("check content of tuned file /usr/lib/tuned/openshift-node/tuned.conf to match default NTO settings")
		openshiftNodeTunedConf, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift-node/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftNodeTunedConf).To(o.And(
			o.ContainSubstring("include=openshift"),
			o.ContainSubstring("net.ipv4.tcp_fastopen=3"),
			o.ContainSubstring("fs.inotify.max_user_watches=65536"),
			o.ContainSubstring("fs.inotify.max_user_instances=8192")))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:33238][OTP]support operatorapi Removed state [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		g.By("remove custom profile (if not already removed) and patch default tuned back to Managed")

		ntoRes := utils.NtoResource{
			Name:        "tuning-pidmax",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "custom-tuned-profiles-node.yaml"),
			SysctlParam: "kernel.pid_max",
			SysctlValue: "182218",
		}

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Cleanup and switch NTO back to the managed state
		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch-").Execute()
			_ = utils.PatchTunedState(oc, ntoNamespace, "default", "Managed")
			_ = ntoRes.Delete(oc)
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with tuned.openshift.io/elasticsearch=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/elasticsearch=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("the tuned name on node %v is %v", tunedNodeName, tunedPodName)

		// Apply new profile that match label tuned.openshift.io/elasticsearch=
		g.By("create new profile from CR")
		err = ntoRes.CreateCustomTunedProfile(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("tuning-pidmax"))

		g.By("check logs, profile changes SHOULD be applied since tuned is MANAGED")
		logsCheck, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, "--tail=9", tunedPodName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(logsCheck).To(o.ContainSubstring("tuning-pidmax"))

		g.By("compare the value kernel.pid_max on node with labeled node, should be 182218")
		err = utils.CompareSysctlValueOnAllWorkerNodesWithRetry(ctx, oc, tunedNodeName, "kernel.pid_max", "", "182218")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("patch default tuned to 'Removed'")
		err = utils.PatchTunedState(oc, ntoNamespace, "default", "Removed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err := utils.GetTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Removed"))

		g.By("check logs, profiles, and nodes (profile changes SHOULD NOT be applied since tuned is REMOVED)")

		g.By("check pod status, all tuned pod should be terminated since tuned is REMOVED")
		err = utils.WaitForNoPodsAvailableByKind(ctx, oc, "daemonset", "tuned", ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		podCheck, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "pods").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podCheck).NotTo(o.ContainSubstring("tuned"))

		g.By("check profile status, all node profile should be removed since tuned is REMOVED)")
		profileCheck, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io", "-o", "template", "--template={{len .items}}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("0"))

		g.By("change tuned state back to managed")
		err = utils.PatchTunedState(oc, ntoNamespace, "default", "Managed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = utils.GetTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Managed"))

		g.By("get the tuned pod name")
		tunedPodName, err = utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check logs, profiles, and nodes (profile changes SHOULD be applied since tuned is MANAGED)")
		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("tuning-pidmax"))

		g.By("check logs, profile changes SHOULD be applied since tuned is MANAGED)")
		logsCheck, err = oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, "--tail=9", tunedPodName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(logsCheck).To(o.ContainSubstring("tuning-pidmax"))

		g.By("compare the value kernel.pid_max on node with labeled node, should be 182218")
		err = utils.CompareSysctlValueOnAllWorkerNodesWithRetry(ctx, oc, tunedNodeName, "kernel.pid_max", "", "182218")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:30589][OTP]use machine configs to apply kernel parameters for realtime tuned profiles [Disruptive][Slow]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		tunedNodeName, pool, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if ocpArch == "ppc64le" {
			g.Skip("NTO with realtime is not supported on ppc64le, skipping test")
		}

		initialMachineCount, err := utils.GetPoolUpdatedMachineCount(ctx, oc, pool)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-rt-").Execute()
			_ = utils.WaitForPoolUpdatedMachineCount(cleanupCtx, oc, pool, initialMachineCount)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-realtime", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-rt", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("create machine config pool")
		err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "machine-config-pool.yaml"), "-p", "MCP_NAME=worker-rt")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("label the node with node-role.kubernetes.io/worker-rt=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-rt=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-realtime profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "realtime.yaml"), "-p", "INCLUDE=openshift-node,realtime")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("assert if machine config pool applied for worker nodes")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker", 600)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-rt", 600)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if openshift-realtime profile was applied")
		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-realtime")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("openshift-realtime"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("assert if isolcpus was applied in machineconfig")
		err = utils.AssertTunedAppliedMC(oc, "nto-worker-rt", "isolcpus=")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if isolcpus was applied in labeled node")
		isMatch, err := utils.AssertTunedAppliedToNode(oc, tunedNodeName, "isolcpus=")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isMatch).To(o.Equal(true))

		g.By("delete openshift-realtime tuned in labeled node")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-realtime", "-n", ntoNamespace, "--ignore-not-found").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check Nodes for expected changes")
		_, err = utils.WaitForSchedulingDisabledNode(ctx, oc, "")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if machine config pool applied for worker nodes")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-rt", 600)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("assert if isolcpus was applied in labeled node")
		isMatch, err = utils.AssertTunedAppliedToNode(oc, tunedNodeName, "isolcpus=")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isMatch).To(o.Equal(false))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:29804][OTP]update tuned profiles after fixing incorrect custom resource configurations [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		var (
			tunedNodeName string
			err           error
		)

		// Use the last worker node as labeled node
		// Support 3 master/worker node, no dedicated worker nodes
		tunedNodeName, _, err = utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		utils.Logf("tunedNodeName is:\n%v", tunedNodeName)

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "ips", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with tuned=ips")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned=ips", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create ips-host profile, new tuned should automatically handle duplicate sysctl settings")
		// Define duplicated parameter and value
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "ips.yaml"), "-p", "SYSCTLPARM1=kernel.pid_max", "SYSCTLVALUE1=1048575", "SYSCTLPARM2=kernel.pid_max", "SYSCTLVALUE2=1048575")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert recommended profile (ips-host) matches current configuration in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "15", 180, `'ips-host' applied|recommended profile \(ips-host\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new custom profile applied to label node")
		ok, err := utils.AssertNTOCustomProfileStatus(oc, ntoNamespace, tunedNodeName, "ips-host", "True", "False")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ok).To(o.Equal(true))

		// Only used for debug info
		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// New tuned can automatically de-duplicate value of sysctl, no duplicate error anymore
		g.By("assert if the duplicate value of sysctl kernel.pid_max takes effect on target node, expected value should be 1048575")
		err = utils.CompareSpecifiedValueByNameOnLabelNode(ctx, oc, tunedNodeName, "kernel.pid_max", "1048575")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("get default value of fs.mount-max on label node")
		defaultMaxMapCount, err := utils.GetValueOfSysctlByName(oc, tunedNodeName, "fs.mount-max")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultMaxMapCount).NotTo(o.BeEmpty())
		utils.Logf("the default value of sysctl fs.mount-max is %v", defaultMaxMapCount)

		// setting an invalid value for ips-host profile
		g.By("update ips-host profile with invalid value of fs.mount-max = -1")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "ips.yaml"), "-p", "SYSCTLPARM1=fs.mount-max", "SYSCTLVALUE1=-1", "SYSCTLPARM2=kernel.pid_max", "SYSCTLVALUE2=1048575")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert 'ips-host' applied in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "20", 180, `recommended profile \(ips-host\) matches current configuration|'ips-host' applied`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new custom profile applied to label node")
		ok, err = utils.AssertNTOCustomProfileStatus(oc, ntoNamespace, tunedNodeName, "ips-host", "True", "True")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ok).To(o.Equal(true))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// The invalid value won't impact default value of fs.mount-max
		g.By("assert if the value of sysctl fs.mount-max still use default value")
		err = utils.CompareSpecifiedValueByNameOnLabelNode(ctx, oc, tunedNodeName, "fs.mount-max", defaultMaxMapCount)
		o.Expect(err).NotTo(o.HaveOccurred())

		// setting an new value of fs.mount-max for ips-host profile
		g.By("update ips-host profile with new value of fs.mount-max = 868686")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "ips.yaml"), "-p", "SYSCTLPARM1=fs.mount-max", "SYSCTLVALUE1=868686", "SYSCTLPARM2=kernel.pid_max", "SYSCTLVALUE2=1048575")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert recommended profile (ips-host) matches current configuration in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "15", 180, `recommended profile \(ips-host\) matches current configuration|'ips-host' applied`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new custom profile applied to label node")
		ok, err = utils.AssertNTOCustomProfileStatus(oc, ntoNamespace, tunedNodeName, "ips-host", "True", "False")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ok).To(o.Equal(true))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// The invalid value won't impact default value of fs.mount-max
		g.By("assert if the new value of sysctl fs.mount-max takes effect, expected value is 868686")
		err = utils.CompareSpecifiedValueByNameOnLabelNode(ctx, oc, tunedNodeName, "fs.mount-max", "868686")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	// [Timeout:30m] applied because this test runs very close to the 15-minute default.
	g.It("[test_id:39123][OTP]update tuned profiles after changing an included profile [Disruptive][Slow][Timeout:30m]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		workerInitialMachineCount, err := utils.GetPoolUpdatedMachineCount(ctx, oc, "worker")
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedNodeName, pool, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-cnf-").Execute()
			_ = utils.WaitForPoolUpdatedMachineCount(cleanupCtx, oc, pool, workerInitialMachineCount)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "performance-patch", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("PerformanceProfile", "performance", "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-cnf", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("label the node with node-role.kubernetes.io/worker-cnf=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-cnf=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			// Only GCP and AWS support realtime-kernel
			g.By("apply performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-performanceprofile.yaml"), "-p", "ISENABLED=true")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else if ocpArch == "ppc64le" {
			g.By("apply pao-baseprofile performance profile for ppc64le")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile-ppc64le.yaml"), "-p", "ISENABLED=false")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			g.By("apply performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-performanceprofile.yaml"), "-p", "ISENABLED=false")
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		g.By("apply worker-cnf machineconfigpool")
		err = utils.CreateOperatorResourceByYaml(oc, paoNamespace, fx.file("pao", "pao-workercnf-mcp.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the MCP worker-cnf has been successfully applied")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-cnf", 900)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile openshift-node-performance-performance was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node-performance-performance")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if profile openshift-node-performance-performance applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-performance"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if tuned pod logs contains openshift-node-performance-performance on labeled nodes")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "20", 60, "openshift-node-performance-performance")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if the linux kernel parameter as vm.stat_interval = 10")
		err = utils.CompareSpecifiedValueByNameOnLabelNode(ctx, oc, tunedNodeName, "vm.stat_interval", "10")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("apply performance-patch profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("pao", "pao-performance-patch.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the MCP worker-cnf is ready after node rebooted")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-cnf", 750)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if the active profile is applied on nodes")
		nodeProfileName, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-performance"))

		g.By("check if tuned pod logs contains Cannot find profile 'openshift-node-performance-example-performanceprofile' on labeled nodes")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "30", 60, "Cannot find profile 'openshift-node-performance-example-performanceprofile'")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if the linux kernel parameter as vm.stat_interval = 10")
		err = utils.CompareSpecifiedValueByNameOnLabelNode(ctx, oc, tunedNodeName, "vm.stat_interval", "10")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("patch include to include=openshift-node-performance-performance")
		err = utils.PatchTunedProfile(oc, ntoNamespace, "performance-patch", fx.file("pao", "pao-performance-fixpatch.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the MCP worker-cnf is ready after node rebooted")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-cnf", 600)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile performance-patch was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "performance-patch")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if contains 'performance-patch' applied in tuned pod logs on labeled nodes")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "30", 60, `recommended profile \(performance-patch\) matches current configuration|'performance-patch' applied`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if the linux kernel parameter as vm.stat_interval = 10")
		err = utils.CompareSpecifiedValueByNameOnLabelNode(ctx, oc, tunedNodeName, "vm.stat_interval", "10")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:45686][OTP]create and apply a tuned profile after the referenced performance profile is created [Disruptive][Slow]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		workerInitialMachineCount, err := utils.GetPoolUpdatedMachineCount(ctx, oc, "worker")
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedNodeName, pool, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-optimize-").Execute()
			_ = utils.WaitForPoolUpdatedMachineCount(cleanupCtx, oc, pool, workerInitialMachineCount)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "include-performance-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("PerformanceProfile", "optimize", "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-optimize", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get the tuned pod name in the labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("label the node with node-role.kubernetes.io/worker-optimize=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-optimize=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("apply worker-optimize machineconfigpool")
		err = utils.CreateOperatorResourceByYaml(oc, paoNamespace, fx.file("pao", "pao-workeroptimize-mcp.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the MCP has been successfully applied")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-optimize", 600)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("apply include-performance-profile tuned profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-include-performance-profile.yaml"), "-p", "ROLENAME=worker-optimize")
		o.Expect(err).NotTo(o.HaveOccurred())
		g.By("assert if the mcp is ready after server has been successfully rebooted")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-optimize", 600)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if the active profile is applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node"))

		g.By("check if tuned pod logs contains Cannot find profile 'openshift-node-performance-optimize' on labeled nodes")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "10", 60, "Cannot find profile 'openshift-node-performance-optimize'")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("apply performance optimize profile")
		err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-performance-optimize.yaml"), "-p", "ROLENAME=worker-optimize")
		o.Expect(err).NotTo(o.HaveOccurred())
		g.By("assert if the mcp is ready after server has been successfully rebooted")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-optimize", 600)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check performance profile tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-performance-optimize"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if new NTO profile performance-patch was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "include-performance-profile")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if the active profile is applied on nodes")
		nodeProfileName, err = utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("include-performance-profile"))

		g.By("check if contains 'include-performance-profile' applied in tuned pod logs on labeled nodes")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "20", 60, `'include-performance-profile' applied|recommended profile \(include-performance-profile\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// This test can run in Parallel with other tests and is not Disruptive.
	// author: liqcui@redhat.com
	g.It("[test_id:36152][OTP]expose metrics and alerts via the NTO metrics endpoint", func(ctx context.Context) {
		// Get metric information that require ssl auth.
		sslKey := "/etc/prometheus/secrets/metrics-client-certs/tls.key"
		sslCrt := "/etc/prometheus/secrets/metrics-client-certs/tls.crt"

		// Get NTO metrics data.
		g.By("get NTO metrics information without ssl, should be denied access, throw error")
		metricsOutput, _ := oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", "openshift-monitoring", "sts/prometheus-k8s", "-c", "prometheus", "--", "curl", "-k", "https://node-tuning-operator.openshift-cluster-node-tuning-operator.svc:60000/metrics").Output()
		o.Expect(metricsOutput).NotTo(o.BeEmpty())
		o.Expect(metricsOutput).To(o.Or(
			o.ContainSubstring("bad certificate"),
			o.ContainSubstring("errno = 104"),
			o.ContainSubstring("certificate required"),
			o.ContainSubstring("error:1409445C"),
			o.ContainSubstring("exit code 56"),
			o.ContainSubstring("Unauthorized"),
			o.ContainSubstring("errno = 32")))

		g.By("get NTO metrics information with ssl key and crt, should be access, get the metric information")
		metricsOutput, metricsError := oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", "openshift-monitoring", "sts/prometheus-k8s", "-c", "prometheus", "--", "curl", "-k", "--key", sslKey, "--cert", sslCrt, "https://node-tuning-operator.openshift-cluster-node-tuning-operator.svc:60000/metrics").Output()
		o.Expect(metricsOutput).NotTo(o.BeEmpty())
		o.Expect(metricsError).NotTo(o.HaveOccurred())

		utils.Logf("the metrics information of NTO as below: \n%v", metricsOutput)

		// Assert the key metrics.
		g.By("check if all metrics exist as expected")
		o.Expect(metricsOutput).To(o.And(
			o.ContainSubstring("nto_build_info"),
			o.ContainSubstring("nto_pod_labels_used_info"),
			o.ContainSubstring("nto_degraded_info"),
			o.ContainSubstring("nto_profile_calculated_total")))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49265][OTP]support automatic rotation of SSL certificates [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		if utils.IsSNOOrCompact(oc) {
			g.Skip("Single Node or Compact Cluster - skipping test")
		}

		g.DeferCleanup(func(cleanupCtx context.Context) {
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		utils.Logf("the tuned node name is: \n%v", tunedNodeName)

		// Get NTO operator pod name
		ntoOperatorPod, err := utils.GetNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("the tuned operator pod name is: \n%v", ntoOperatorPod)

		metricEndpoint, err := utils.GetServiceEndpoint(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("get information about the certificate the metrics server in NTO")
		var certificateBefore string
		pollCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		err = wait.PollUntilContextTimeout(pollCtx, 15*time.Second, 180*time.Second, false, func(_ context.Context) (bool, error) {
			certificateBefore, err = utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "/bin/bash", "-c", "/bin/openssl s_client -connect "+metricEndpoint+" 2>/dev/null </dev/null | /bin/openssl x509")
			if err != nil {
				utils.Logf("failed to get certificate from %v: %v, retrying", tunedNodeName, err)
				return false, nil
			}
			return true, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "failed to get openssl certificate information after retries")
		utils.Logf("the certificate of NTO metrics server before rotate as below: \n%v", certificateBefore)

		encodeBase64CertificateBefore := utils.StringToBASE64(certificateBefore)

		// To improve the success rate, execute oc delete secret/node-tuning-operator-tls instead of oc -n openshift-service-ca secret/signing-key
		// The last one "oc -n openshift-service-ca secret/signing-key" take more time to complete, but need to manually execute once failed.
		g.By("delete secret/node-tuning-operator-tls to automatically create a new certificate")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "secret/node-tuning-operator-tls").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if NTO rotate certificates")
		err = utils.WaitForNTOCertificateRotation(ctx, oc, ntoNamespace, tunedNodeName, encodeBase64CertificateBefore)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("the certificate extracted from the openssl command should match the first certificate from the tls.crt file in the secret")
		err = utils.CompareCertificateBetweenOpenSSLandTLSSecret(ctx, oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49371][OTP]not restart the tuned daemon when profile application takes too long [Disruptive][Slow]", oteg.Informing(), func(ctx context.Context) {
		// Automatic tuned daemon restart was removed due to timeout in the bug https://issues.redhat.com/browse/OCPBUGS-30647

		// Use the first worker node as labeled node
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "worker-stuck-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-profile-stuck", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with worker-stuck=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "worker-stuck=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-profile-stuck profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "worker-stuck-tuned.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check openshift-profile-stuck tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-profile-stuck"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("assert recommended profile (openshift-profile-stuck) matches current configuration in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "12", 300, `'openshift-profile-stuck' applied|recommended profile \(openshift-profile-stuck\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile openshift-profile-stuck was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-profile-stuck")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if the active profile is applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-profile-stuck"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("verify the tuned pod log does not contain [ timeout (120) to apply TuneD profile; restarting TuneD daemon ]")
		ntoPodLogs, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, tunedPodName, "--tail=10").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ntoPodLogs).NotTo(o.ContainSubstring("timeout (120) to apply TuneD profile; restarting TuneD daemon"))

		g.By("verify the tuned pod log does not contain [ error waiting for tuned: signal: terminated ]")
		ntoPodLogs, err = oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, tunedPodName, "--tail=10").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ntoPodLogs).NotTo(o.ContainSubstring("error waiting for tuned: signal: terminated"))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49370][OTP]support hugepages via the bootloader plug-in [Disruptive][Slow]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		tunedNodeName, pool, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		initialMachineCount, err := utils.GetPoolUpdatedMachineCount(ctx, oc, pool)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-hp-").Execute()
			_ = utils.WaitForPoolUpdatedMachineCount(cleanupCtx, oc, pool, initialMachineCount)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "hugepages", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-hp", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with node-role.kubernetes.io/worker-hp=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-hp=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create hugepages tuned profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "hugepage-tuned-boottime.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check hugepages tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("hugepages"))

		g.By("create worker-hp machineconfigpool")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "hugepage-mcp.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the MCP has been successfully applied")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-hp", 720)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node-hugepages")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if profile openshift-node-hugepages applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-hugepages"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check value of allocatable.hugepages-2Mi in labeled node")
		nodeHugePagesOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable.hugepages-2Mi}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeHugePagesOutput).To(o.ContainSubstring("100M"))

		g.DeferCleanup(oc.TeardownProject)
		err = oc.SetupProject()
		o.Expect(err).NotTo(o.HaveOccurred())
		ntoTestNS := oc.Namespace()

		// Create a hugepages-app application pod
		g.By("create a hugepages-app pod to consume hugepage in nto temp namespace")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "hugepage-100m-pod.yaml"), "-p", "IMAGENAME="+nginxAlpine)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Check if hugepages-app is ready
		g.By("check if a hugepages-app pod is ready")
		err = utils.AssertPodToBeReady(ctx, oc, "hugepages-app", ntoTestNS)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the value of /etc/podinfo/hugepages_2M_request, the value expected is 105")
		podInfo, err := utils.RemoteShPod(oc, ntoTestNS, "hugepages-app", "cat", "/etc/podinfo/hugepages_2M_request")
		utils.Logf("PodInfo is: \n%v", podInfo)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podInfo).To(o.ContainSubstring("105"))

		g.By("check the value of REQUESTS_HUGEPAGES in env on pod")
		envInfo, err := utils.RemoteShPodWithBash(oc, ntoTestNS, "hugepages-app", "env | grep REQUESTS_HUGEPAGES")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(envInfo).To(o.ContainSubstring("REQUESTS_HUGEPAGES_2Mi=104857600"))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49439][OTP]start and stop the stalld service via tuned service plug-in [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		// Use the first rhcos worker node as labeled node
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("tunedNodeName is [ %v ]", tunedNodeName)

		if len(tunedNodeName) == 0 {
			g.Skip("Skip Testing on RHEL worker or windows node")
		}

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-stalld", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_, _ = utils.DebugNodeWithChroot(oc, tunedNodeName, "/usr/bin/throttlectl", "on")
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("set off for /usr/bin/throttlectl before enable stalld")
		err = utils.SwitchThrottlectlOnOff(ctx, oc, ntoNamespace, tunedNodeName, "off", 30)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("label the node with node-role.kubernetes.io/worker-stalld=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-stalld tuned profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "stalld-tuned.yaml"), "-p", "STALLD_STATUS=start,enable")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check openshift-stalld tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-stalld")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if profile openshift-stalld applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if stalld service is running")
		stalldStatus, err := utils.DebugNodeWithChroot(oc, tunedNodeName, "systemctl", "status", "stalld")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldStatus).To(o.ContainSubstring("active (running)"))

		g.By("apply openshift-stalld with stop,disable tuned profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "stalld-tuned.yaml"), "-p", "STALLD_STATUS=stop,disable")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-stalld")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if stalld service is inactive and stopped")
		stalldStatus, _ = utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"-q", "--to-namespace", ntoNamespace}, "systemctl", "status", "stalld")
		o.Expect(stalldStatus).NotTo(o.BeEmpty())
		o.Expect(stalldStatus).To(o.ContainSubstring("inactive (dead)"))

		g.By("apply openshift-stalld with start,enable tuned profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "stalld-tuned.yaml"), "-p", "STALLD_STATUS=start,enable")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-stalld")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if stalld service is running again")
		stalldStatus, _, err = utils.DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace", ntoNamespace}, "systemctl", "status", "stalld")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldStatus).NotTo(o.BeEmpty())
		o.Expect(stalldStatus).To(o.ContainSubstring("active (running)"))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49441][OTP]apply a profile with multiple inheritance where parent profiles share a common ancestor [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		// trying to include two profiles that share the same parent profile "throughput-performance". An example of such profiles
		// are the openshift-node --> openshift --> (virtual-guest) --> throughput-performance and postgresql profiles.
		// Use the first worker node as labeled node

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/openshift-node-postgresql-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-node-postgresql", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with tuned.openshift.io/openshift-node-postgresql=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/openshift-node-postgresql=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check postgresql profile /usr/lib/tuned/postgresql/tuned.conf include throughput-performance profile")
		postGreSQLProfile, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/postgresql/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(postGreSQLProfile).To(o.ContainSubstring("throughput-performance"))

		g.By("check postgresql profile /usr/lib/tuned/openshift-node/tuned.conf include openshift profile")
		openshiftNodeProfile, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift-node/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftNodeProfile).To(o.ContainSubstring(`include=openshift`))

		g.By("check postgresql profile /usr/lib/tuned/openshift/tuned.conf include throughput-performance profile")
		openshiftProfile, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftProfile).To(o.ContainSubstring("throughput-performance"))

		g.By("create openshift-node-postgresql tuned profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "openshift-node-postgresql.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check openshift-node-postgresql tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-postgresql"))

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node-postgresql")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if profile openshift-node-postgresql applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-postgresql"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("assert recommended profile (openshift-node-postgresql) matches current configuration in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "10", 300, `'openshift-node-postgresql' applied|recommended profile \(openshift-node-postgresql\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	g.It("[test_id:49705][OTP]handle network devices with n/a channel values using the net plug-in [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		if iaasPlatform == "vsphere" || iaasPlatform == "openstack" || iaasPlatform == "none" || iaasPlatform == "powervs" {
			g.Skip("IAAS platform: " + iaasPlatform + " does not support cloud provider profile - skipping test")
		}

		isSNO := utils.IsSNOCluster(oc)
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check default channel for host network adapter, not expected Combined: 1, if so, skip testing")
		// utils.AssertNetworkChannelQueuesStatus is used for checking if match Combined: 1
		// If match <Combined: 1>, skip testing
		isMatch, err := utils.AssertNetworkChannelQueuesStatus(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		if isMatch {
			g.Skip("Only one NIC queues or Unsupported NIC - skipping test")
		}

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/netplugin-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "net-plugin", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with node-role.kubernetes.io/netplugin=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/netplugin=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create net-plugin tuned profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "net-plugin-tuned-node.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check net-plugin tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("net-plugin"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("assert tuned.plugins.base: instance net: assigning devices match in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "180", 300, "tuned.plugins.base: instance net: assigning devices")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert active and recommended profile (net-plugin) match in tuned pod log")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, tunedPodName, "180", 300, `'net-plugin' applied|recommended profile \(net-plugin\) matches current configuration`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "net-plugin")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if profile net-plugin applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(nodeProfileName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("net-plugin"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check channel for host network adapter, expected Combined: 1")
		result, err := utils.AssertNetworkChannelQueuesStatus(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.BeTrue())

		g.By("delete tuned net-plugin and check channel for host network adapter again")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "net-plugin", "-n", ntoNamespace, "--ignore-not-found").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if profile openshift-node|openshift-control-plane applied on nodes")
		if isSNO {
			err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-control-plane")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node")
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check channel for host network adapter, not expected Combined: 1")
		result, err = utils.AssertNetworkChannelQueuesStatus(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.BeFalse())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49617][OTP]support cloud-provider specific profiles [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		if iaasPlatform == "none" {
			g.Skip("IAAS platform: " + iaasPlatform + " does not support cloud provider profile - skipping test")
		}

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("get cloud provider name")
		providerName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(providerName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-"+providerName, "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-abc", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		providerID, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.spec.providerID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerID).NotTo(o.BeEmpty())
		o.Expect(providerID).To(o.ContainSubstring(providerName))

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value should be 8192")
		sysctlOutput, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "sysctl", "vm.admin_reserve_kbytes")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(sysctlOutput).NotTo(o.BeEmpty())
		o.Expect(sysctlOutput).To(o.ContainSubstring("vm.admin_reserve_kbytes = 8192"))

		g.By("apply cloud-provider profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "cloud-provider-profile.yaml"), "-p", "PROVIDER_NAME="+providerName)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check /var/lib/tuned/provider on target nodes")
		openshiftProfile, err := utils.RemoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/var/lib/ocp-tuned/provider")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftProfile).NotTo(o.BeEmpty())
		o.Expect(openshiftProfile).To(o.ContainSubstring(providerName))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check tuned for NTO")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned.tuned.openshift.io").Output()
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("current tuned for NTO: \n%v", output)

		g.By("check provider + providerName profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("provider-" + providerName))

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value is 16386")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "16386")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("remove cloud-provider profile, the value of vm.admin_reserve_kbytes rolls back to 8192")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-"+providerName, "-n", ntoNamespace).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value should be 8192")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "8192")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("apply cloud-provider-abc profile, where abc does not belong to any cloud provider")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "cloud-provider-profile.yaml"), "-p", "PROVIDER_NAME=abc")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value should be no change, still is 8192")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "8192")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// This test can run in Parallel with other tests and is not Disruptive.
	// author: liqcui@redhat.com
	g.It("[test_id:45593][OTP]set the correct io_timeout for AWS Nitro instances", func(ctx context.Context) {
		var err error
		// currently test is only supported on AWS
		if iaasPlatform == "aws" {
			g.By("expect /sys/module/nvme_core/parameters/io_timeout value on each node: 4294967295")
			err = utils.AssertIOTimeoutAndMaxRetries(ctx, oc)
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			g.Skip("Test Case 45593 does not support other cloud platforms, only AWS - skipping test")
		}
	})

	// author: liqcui@redhat.com
	g.It("[test_id:27420][OTP]provide the default tuned resource [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		g.DeferCleanup(func(cleanupCtx context.Context) {
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		defaultTunedCreateTimeBefore, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedCreateTimeBefore).NotTo(o.BeEmpty())

		g.By("delete the default tuned")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "default", "-n", ntoNamespace).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		g.By("make sure the tuned default is created and ready")
		err = utils.ConfirmedTunedReady(ctx, oc, ntoNamespace, "default", 60)
		o.Expect(err).NotTo(o.HaveOccurred())

		defaultTunedCreateTimeAfter, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedCreateTimeAfter).NotTo(o.BeEmpty())
		o.Expect(defaultTunedCreateTimeAfter).NotTo(o.ContainSubstring(defaultTunedCreateTimeBefore))

		defaultTunedCreateTimeBefore, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(defaultTunedCreateTimeBefore).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		defaultTunedCreateTimeAfter, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedCreateTimeAfter).NotTo(o.BeEmpty())
		o.Expect(defaultTunedCreateTimeAfter).To(o.ContainSubstring(defaultTunedCreateTimeBefore))

		utils.Logf("defaultTunedCreateTimeBefore is : %v defaultTunedCreateTimeAfter is: %v", defaultTunedCreateTimeBefore, defaultTunedCreateTimeAfter)
	})

	// This test can run in Parallel with other tests and is not Disruptive.
	// author: liqcui@redhat.com
	g.It("[test_id:41552][OTP]report per-node tuned profile application status", func(ctx context.Context) {
		isSNOOrCompact := utils.IsSNOOrCompact(oc)
		masterNodeName, err := utils.GetFirstMasterNodeName(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		defaultMasterProfileName, err := utils.GetDefaultProfileNameOnMaster(oc, masterNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())

		// NTO will provide two default tuned profiles, one is openshift-control-plane, the other is openshift-node
		g.By("check the default tuned profile list per nodes")
		profileOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileOutput).NotTo(o.BeEmpty())
		if isSNOOrCompact {
			o.Expect(profileOutput).To(o.ContainSubstring(defaultMasterProfileName))
		} else {
			o.Expect(profileOutput).To(o.ContainSubstring("openshift-control-plane"))
			o.Expect(profileOutput).To(o.ContainSubstring("openshift-node"))
		}
	})

	// author: liqcui@redhat.com
	g.It("[test_id:50052][OTP]run the stalld service with SCHED_FIFO scheduling policy [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		if iaasPlatform == "vsphere" || iaasPlatform == "none" {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test")
		}

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("tunedNodeName is [ %v ]", tunedNodeName)

		if len(tunedNodeName) == 0 {
			g.Skip("Skip Testing on RHEL worker or windows node")
		}

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-stalld", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_, _ = utils.DebugNodeWithChroot(oc, tunedNodeName, "/usr/bin/throttlectl", "on")
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Switch off throttlectl to improve successful rate of stalld starting
		g.By("set off for /usr/bin/throttlectl before enable stalld")
		err = utils.SwitchThrottlectlOnOff(ctx, oc, ntoNamespace, tunedNodeName, "off", 30)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("label the node with node-role.kubernetes.io/worker-stalld=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-stalld tuned profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "stalld-tuned.yaml"), "-p", "STALLD_STATUS=start,enable")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check openshift-stalld tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if new NTO profile was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-stalld")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if profile openshift-stalld applied on nodes")
		nodeProfileName, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).NotTo(o.BeEmpty())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check if stalld service is running")
		stalldStatus, _, err := utils.DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "systemctl", "status", "stalld")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldStatus).NotTo(o.BeEmpty())
		o.Expect(stalldStatus).To(o.ContainSubstring("active (running)"))

		g.By("get stalld PID on labeled node")
		stalldPIDStatus, _, err := utils.DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "/bin/bash", "-c", "ps -efZ | grep stalld | grep -v grep")
		utils.Logf("stalldPIDStatus is :\n%v", stalldPIDStatus)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldPIDStatus).NotTo(o.BeEmpty())
		o.Expect(stalldPIDStatus).NotTo(o.ContainSubstring("unconfined_service_t"))
		o.Expect(stalldPIDStatus).To(o.ContainSubstring("-t 20"))

		g.By("get stalld PID on labeled node")
		stalldPID, _, err := utils.DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "/bin/bash", "-c", "ps -efL| grep stalld | grep -v grep | awk '{print $2}'")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldPID).NotTo(o.BeEmpty())

		g.By("get status of chrt -p stalld PID on labeled node")
		chrtStalldPIDOutput, _, err := utils.DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "/bin/bash", "-c", "chrt -ap "+stalldPID)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(chrtStalldPIDOutput).NotTo(o.BeEmpty())
		o.Expect(chrtStalldPIDOutput).To(o.ContainSubstring("SCHED_FIFO"))
		utils.Logf("chrtStalldPIDOutput is :\n%v", chrtStalldPIDOutput)
	})

	// author: liqcui@redhat.com
	g.It("[test_id:51495][OTP]support basic PAO functionality [Disruptive][Slow]", oteg.Informing(), func(ctx context.Context) {
		// on a worker with arch = ppc64le, pao-baseprofile-ppc64le.yaml is used instead to account for supported kernel parameters (tsc is x86_64 only)

		SkipIsSNO(oc)

		tunedNodeName, pool, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		initialMachineCount, err := utils.GetPoolUpdatedMachineCount(ctx, oc, pool)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()
			_ = utils.WaitForPoolUpdatedMachineCount(cleanupCtx, oc, pool, initialMachineCount)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("performanceprofile", "pao-baseprofile", "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-pao", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get how many cpus on the specified worker node
		g.By("get the number of CPU cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node does not have enough cpus - skipping test")
		}
		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("label the node with node-role.kubernetes.io/worker-pao=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			// Only GCP and AWS support realtime-kernel
			g.By("apply pao-baseprofile performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile.yaml"), "-p", "ISENABLED=true")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else if ocpArch == "ppc64le" {
			g.By("apply pao-baseprofile performance profile for ppc64le")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile-ppc64le.yaml"), "-p", "ISENABLED=false")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			g.By("apply pao-baseprofile performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile.yaml"), "-p", "ISENABLED=false")
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		g.By("check Performance Profile pao-baseprofile was created automatically")
		paoBasePerformanceProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(paoBasePerformanceProfile).NotTo(o.BeEmpty())
		o.Expect(paoBasePerformanceProfile).To(o.ContainSubstring("pao-baseprofile"))

		g.By("create machine config pool worker-pao")
		err = utils.CreateOperatorResourceByYaml(oc, "", fx.file("pao", "pao-baseprofile-mcp.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if machine config pool applied for worker nodes")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-pao", 1200)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("verify PAO profile was applied correctly")
		expectRT := (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64"
		err = utils.VerifyPAOProfile(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile", "pao-baseprofile", expectRT)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check allocatable system resource on labeled node")
		allocatableResource, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(allocatableResource).NotTo(o.BeEmpty())
		utils.Logf("the allocatable system resource on labeled node: \n%v", allocatableResource)

		g.DeferCleanup(oc.TeardownProject)
		err = oc.SetupProject()
		o.Expect(err).NotTo(o.HaveOccurred())
		ntoTestNS := oc.Namespace()

		// Create a guaranteed-pod application pod
		g.By("create a guaranteed-pod pod into temp namespace")
		err = utils.CreateOperatorResourceByYaml(oc, ntoTestNS, fx.file("pao", "pao-baseqos-pod.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		// Check if guaranteed-pod is ready
		g.By("check if a guaranteed-pod pod is ready")
		err = utils.AssertPodToBeReady(ctx, oc, "guaranteed-pod", ntoTestNS)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the cpu bind to isolation CPU zone for a guaranteed-pod")
		cpuManagerStateOutput, err := utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "/bin/bash", "-c", "cat /var/lib/kubelet/cpu_manager_state")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerStateOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerStateOutput).To(o.ContainSubstring("guaranteed-pod"))
		utils.Logf("the settings of CPU Manager cpuManagerState on labeled nodes: \n%v", cpuManagerStateOutput)
	})

	// author: liqcui@redhat.com
	g.It("[test_id:53053][OTP]automatically delete profiles not bound to any nodes [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		if iaasPlatform == "none" {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test")
		}

		// Get NTO operator pod name
		ntoOperatorPod, err := utils.GetNTOPodName(oc, ntoNamespace)
		o.Expect(ntoOperatorPod).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("get cloud provider name")
		providerName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerName).NotTo(o.BeEmpty())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("profiles.tuned.openshift.io", "worker-does-not-exist-openshift-node", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("apply worker-does-not-exist-openshift-node profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "nto-unknown-profile.yaml"), "-p", "PROVIDER_NAME="+providerName)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("the profile worker-does-not-exist-openshift-node will be deleted automatically once created.")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.ContainSubstring("worker-does-not-exist-openshift-node"))

		g.By("assert NTO logs to match key words  Node 'worker-does-not-exist-openshift-node' not found")
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, ntoOperatorPod, "4", 120, " Node \"worker-does-not-exist-openshift-node\" not found")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:59884][OTP]support multiple regular expressions in cgroup blacklist configuration [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		// Get the tuned pod name that run on first worker node
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// TODO: Try to make this more generic not relying on nginx.
		AppImageName := nginxAlpine

		// Get how many cpus on the specified worker node
		g.By("get the number of CPU cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node does not have enough cpus - skipping test")
		}

		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "-n", ntoNamespace, "cgroup-scheduler-blacklist").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node-").Execute()
			if ns := oc.Namespace(); ns != "" {
				_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", "-n", ns, "app-web", "--ignore-not-found").Execute()
			}
			oc.TeardownProject()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		err = oc.SetupProject()
		o.Expect(err).NotTo(o.HaveOccurred())
		ntoTestNS := oc.Namespace()

		g.By("label the specified linux node with label tuned-scheduler-node")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// setting cgroup_ps_blacklist=/kubepods\.slice/kubepods-burstable\.slice/;/system\.slice/
		// the process belonging to /kubepods\.slice/kubepods-burstable\.slice/ or /system\.slice/ can consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		// the process not belonging to /kubepods\.slice/kubepods-burstable\.slice/ or /system\.slice/ cannot consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0 or 0,2-N

		g.By("create pod that detects the value of kernel.pid_max ")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "cgroup-scheduler-besteffort-pod.yaml"), "-p", "IMAGE_NAME="+AppImageName)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Check if nginx pod is ready
		g.By("check if best effort pod is ready")
		err = utils.AssertPodToBeReady(ctx, oc, "app-web", ntoTestNS)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create NTO custom tuned profile cgroup-scheduler-blacklist")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "cgroup-scheduler-blacklist.yaml"), "-p", "PROFILE_NAME=cgroup-scheduler-blacklist", `CGROUP_BLACKLIST=/kubepods\.slice/kubepods-burstable\.slice/;/system\.slice/`)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if NTO custom tuned profile cgroup-scheduler-blacklist was applied")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "cgroup-scheduler-blacklist")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		g.By("verify the cpu allow list in cgroup black list for tuned")
		result, err := utils.AssertProcessInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "tuned", nodeCPUCoresInt)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.Equal(true))

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		g.By("verify the cpu allow list in cgroup black list for chronyd")
		result, err = utils.AssertProcessInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "chronyd", nodeCPUCoresInt)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.Equal(true))

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0 or 0,2-N
		g.By("verify the cpu allow list in cgroup black list for nginx process")
		result, err = utils.AssertProcessExcludedFromCgroupScheduler(oc, tunedNodeName, ntoNamespace, "nginx", nodeCPUCoresInt)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.Equal(true))
	})

	// author: liqcui@redhat.com
	// [Timeout:30m] applied because this test runs very close to the 15-minute default.
	g.It("[test_id:60743][OTP]avoid race conditions when updating machine configs for nodes with different CPU counts in the same MCP [Disruptive][Slow][Timeout:30m]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		haveMachineSet, err := utils.MachineSetsExist(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		if !haveMachineSet {
			g.Skip("No machineset found, skipping test")
		}

		if !utils.ImplStringArrayContains(cloudPlatforms, iaasPlatform) {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test")
		}

		tunedNodeName, pool, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		initialMachineCount, err := utils.GetPoolUpdatedMachineCount(ctx, oc, pool)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			g.By(fmt.Sprintf("remove the worker-diff label from %v", tunedNodeName))
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-diffcpus-").Execute()
			g.By("delete ocp-psap-qe-diffcpus machineset")
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("machineset", "ocp-psap-qe-diffcpus", "-n", "openshift-machine-api", "--ignore-not-found").Execute()
			g.By(fmt.Sprintf("wait for %s machine config pool count to equal %v", pool, initialMachineCount))
			_ = utils.WaitForPoolUpdatedMachineCount(cleanupCtx, oc, pool, initialMachineCount)
			g.By("delete openshift-bootcmdline-cpu tuned")
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-bootcmdline-cpu", "-n", ntoNamespace, "--ignore-not-found").Execute()
			g.By("delete custom MCP")
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-diffcpus", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("create openshift-bootcmdline-cpu tuned profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "node-diffcpus-tuned-bootloader.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create machine config pool")
		err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "node-diffcpus-mcp.yaml"), "-p", "MCP_NAME=worker-diffcpus")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("label the last node with node-role.kubernetes.io/worker-diffcpus=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-diffcpus=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create a new machineset with different instance type.")
		newMachinesetInstanceType, err := utils.SpecifyMachinesetWithDifferentInstanceType(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("4 newMachinesetInstanceType is %v, ", newMachinesetInstanceType)
		o.Expect(newMachinesetInstanceType).NotTo(o.BeEmpty())

		err = utils.CreateMachinesetByInstanceType(oc, "ocp-psap-qe-diffcpus", newMachinesetInstanceType)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("wait for new node is ready when machineset created")
		// 1 means replicas=1
		err = utils.WaitForMachinesRunning(ctx, oc, 1, "ocp-psap-qe-diffcpus")
		if err == utils.ErrInsufficientInstanceCapacity || err == utils.ErrInsufficientResources || err == utils.ErrPGClusterPlacementGroupNotSupported {
			g.Skip(err.Error())
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("label the second node with node-role.kubernetes.io/worker-diffcpus=")
		secondTunedNodeName, err := utils.GetNodeNameByMachineset(oc, "ocp-psap-qe-diffcpus")
		o.Expect(err).NotTo(o.HaveOccurred())
		g.DeferCleanup(func() {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondTunedNodeName, "node-role.kubernetes.io/worker-diffcpus-", "--overwrite").Execute()
		})
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondTunedNodeName, "node-role.kubernetes.io/worker-diffcpus=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the status of adding the two worker node into worker-diffcpus mcp, mcp applied")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-diffcpus", 480)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("assert if openshift-bootcmdline-cpu profile was applied")
		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-bootcmdline-cpu")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("openshift-bootcmdline-cpu"))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		ntoOperatorPodName, err := utils.GetNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, ntoOperatorPodName, "25", 180, "Nodes in MCP worker-diffcpus agree on bootcmdline: cpus=")
		o.Expect(err).NotTo(o.HaveOccurred())

		// Comment out with an known issue, until it was fixed
		g.By("assert if cmdline was applied in machineconfig")
		err = utils.AssertTunedAppliedMC(oc, "nto-worker-diffcpus", "cpus=")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if cmdline was applied in labeled node")
		result, err := utils.AssertTunedAppliedToNode(oc, tunedNodeName, "cpus=")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.Equal(true))

		g.By("<Profiles with bootcmdline conflict> warn message will show in oc get co/node-tuning")
		err = utils.WaitForCOStatusWithKeywords(ctx, oc, 60*time.Second, "Profiles with bootcmdline conflict")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// Verify if the <Profiles with bootcmdline conflict> warn message disappears after deactivating custom tuned profile

		g.By(fmt.Sprintf("remove the worker-diff label from %v", tunedNodeName))
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-diffcpus-").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("delete ocp-psap-qe-diffcpus machineset")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("machineset", "ocp-psap-qe-diffcpus", "-n", "openshift-machine-api", "--ignore-not-found").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By(fmt.Sprintf("wait for %s machine config pool count to equal %v", pool, initialMachineCount))
		err = utils.WaitForPoolUpdatedMachineCount(ctx, oc, pool, initialMachineCount)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("<Profiles with bootcmdline conflict> warn message will disappear after removing worker node from mcp worker-diffcpus")
		err = utils.WaitForCONodeTuningStatusClear(ctx, oc, 180, "Profiles with bootcmdline conflict")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By(fmt.Sprintf("assert the 'cpus' kernel command-line is no longer present in /proc/cmdline on %s", tunedNodeName))
		result, err = utils.AssertTunedAppliedToNode(oc, tunedNodeName, "cpus=")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(result).To(o.Equal(false))
	})

	// author: sahshah
	g.It("[test_id:64908][OTP]expose the tuned socket interface for querying active profiles [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		g.By("pick one worker node to label")
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Clean up resources
		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "-n", ntoNamespace, "tuning-maxpid").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Label the node with node-role.kubernetes.io/worker-tuning
		g.By("label the node with node-role.kubernetes.io/worker-tuning=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Apply new profile that match label node-role.kubernetes.io/worker-tuning=
		g.By("create tuning-maxpid profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "tuning-maxpid.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		// NTO will provide two default tuned profiles, one is default
		g.By("check the default tuned list, expect tuning-maxpid")
		allTuneds, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(allTuneds).To(o.ContainSubstring("tuning-maxpid"))

		g.By("check if new profile tuning-maxpid applied to labeled node")
		// Verify if the new profile is applied
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "tuning-maxpid")
		o.Expect(err).NotTo(o.HaveOccurred())
		profileCheck, err := utils.GetTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("tuning-maxpid"))

		g.By("get current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("current profile for each node: \n%v", output)

		g.By("check the custom profile as expected by debugging the node")
		printfString := `printf '{"jsonrpc": "2.0", "method": "active_profile", "id": 1}' | nc -U /run/tuned/tuned.sock`
		printfStringStdOut, err := utils.RemoteShPodWithBash(oc, ntoNamespace, tunedPodName, printfString)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(printfStringStdOut).NotTo(o.BeEmpty())
		o.Expect(printfStringStdOut).To(o.ContainSubstring("tuning-maxpid"))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:65371][OTP]preserve node-level profile settings when the tuned pod is terminated [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(oc.TeardownProject)
		err = oc.SetupProject()
		o.Expect(err).NotTo(o.HaveOccurred())
		ntoTestNS := oc.Namespace()

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with node-role.kubernetes.io/worker-tuning=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create tuning-pidmax profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "nto-tuned-pidmax.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert tuning-pidmax profile applied to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		clusterVersion, _, err := utils.GetClusterVersion(oc)
		utils.Logf("current clusterVersion is [ %v ]", clusterVersion)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(clusterVersion).NotTo(o.BeEmpty())

		g.By("create pod that detects the value of kernel.pid_max ")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "nto-sysctl-pod.yaml"), "-p", "IMAGE_NAME="+nginxAlpine, "RUNASNONROOT=true")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// Check if sysctlpod pod is ready
		err = utils.AssertPodToBeReady(ctx, oc, "sysctlpod", ntoTestNS)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("get the sysctlpod status")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoTestNS, "pods").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("the status of pod sysctlpod: \n%v", output)

		g.By("check the value of kernel.pid_max in the pod sysctlpod, the expected value should be kernel.pid_max = 181818")
		podLogStdout, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args("sysctlpod", "--tail=1", "-n", ntoTestNS).Output()
		utils.Logf("logs of sysctlpod before delete tuned pod is [ %v ]", podLogStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podLogStdout).NotTo(o.BeEmpty())
		o.Expect(podLogStdout).To(o.ContainSubstring("kernel.pid_max = 181818"))

		g.By("delete tuned pod on the labeled node, and make sure kernel.pid_max does not revert to its original value")
		o.Expect(oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", tunedPodName, "-n", ntoNamespace).Execute()).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check tuned pod status after delete tuned pod")
		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err = utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		// Check if tuned pod that deleted is ready
		err = utils.AssertPodToBeReady(ctx, oc, tunedPodName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the value of kernel.pid_max in the pod sysctlpod again, the expected value should still be kernel.pid_max = 181818")
		podLogStdout, err = oc.AsAdmin().WithoutNamespace().Run("logs").Args("sysctlpod", "--tail=2", "-n", ntoTestNS).Output()
		utils.Logf("logs of sysctlpod after delete tuned pod is [ %v ]", podLogStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podLogStdout).NotTo(o.BeEmpty())
		o.Expect(podLogStdout).To(o.ContainSubstring("kernel.pid_max = 181818"))
		o.Expect(podLogStdout).NotTo(o.ContainSubstring("kernel.pid_max not equal 181818"))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49618][OTP]support core PAO and NTO functionality before upgrading the OCP cluster [Disruptive][Slow][Manual]", oteg.Informing(), func() {
		// 49618 is a two-part test.  This is the first part: verify PAO+NTO works before upgrade.  Do not clean up any resources created here.
		// Cleanup will be performed by the second part of the test.

		SkipIsSNO(oc)

		ctx := context.Background()

		if !utils.ImplStringArrayContains(cloudPlatforms, iaasPlatform) {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test")
		}

		totalLinuxWorkerNode, err := utils.CountLinuxWorkerNodes(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		totalLinuxWorkerNodes := strconv.Itoa(totalLinuxWorkerNode)
		if totalLinuxWorkerNode < 2 {
			g.Skip("This test needs at least two worker nodes, have " + totalLinuxWorkerNodes + ", skipping test.")
		}

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get how many cpus on the specified worker node
		g.By("get the number of CPU cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("Current CPU cores of worker node: %v", nodeCPUCoresInt)
		if nodeCPUCoresInt < 4 {
			g.Skip("the worker node does not have enough cpus - skipping test")
		}

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("label the node with node-role.kubernetes.io/worker-pao=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create machine config pool worker-pao")
		err = utils.CreateOperatorResourceByYaml(oc, "", fx.file("pao", "pao-baseprofile-mcp.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-pao", 600)
		o.Expect(err).NotTo(o.HaveOccurred())

		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			// Only GCP and AWS support realtime-kernel
			g.By("apply pao-baseprofile performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile.yaml"), "-p", "ISENABLED=true")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			g.By("apply pao-baseprofile performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile.yaml"), "-p", "ISENABLED=false")
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		g.By("check Performance Profile pao-baseprofile was created automatically")
		paoBasePerformanceProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(paoBasePerformanceProfile).NotTo(o.BeEmpty())
		o.Expect(paoBasePerformanceProfile).To(o.ContainSubstring("pao-baseprofile"))

		g.By("assert if machine config pool applied to worker nodes that label with worker-pao")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-pao", 1800)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.WaitForMCPUpdate(ctx, oc, "worker", 300)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.WaitForMCPUpdate(ctx, oc, "master", 720)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("verify PAO profile was applied correctly")
		expectRT := (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64"
		err = utils.VerifyPAOProfile(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile", "pao-baseprofile", expectRT)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check Kernel boot settings passed into /proc/cmdline in labeled node")
		kernelCMDLineStdout, err := utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "cat", "/proc/cmdline")
		utils.Logf("the settings of Kernel boot passed into /proc/cmdline  on labeled nodes: \n%v", kernelCMDLineStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernelCMDLineStdout).NotTo(o.BeEmpty())
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("tsc=reliable"))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("isolcpus="))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("hugepagesz=1G"))

		// o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("nosmt"))
		//     - nosmt  removed nosmt to improve success rate due to limited cpu cores
		// but manually re-enabled when have enough cpu cores
	})

	// author: liqcui@redhat.com
	g.It("[test_id:49618][OTP]support core PAO and NTO functionality after upgrading the OCP cluster [Disruptive][Slow][Manual]", oteg.Informing(), func(ctx context.Context) {
		// 49618 is a two-part test.  This is the second part: verify PAO+NTO survives upgrade.  Clean-up any resources created
		// by the first part of the test.

		SkipIsSNO(oc)

		if !utils.ImplStringArrayContains(cloudPlatforms, iaasPlatform) {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test")
		}

		totalLinuxWorkerNode, err := utils.CountLinuxWorkerNodes(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		totalLinuxWorkerNodes := strconv.Itoa(totalLinuxWorkerNode)
		if totalLinuxWorkerNode < 2 {
			g.Skip("The total linux worker node is " + totalLinuxWorkerNodes + ". The OCP do not have enough worker node, skip it.")
		}

		tunedNodeNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker-pao", "-ojsonpath={.items[*].metadata.name}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if len(tunedNodeNames) == 0 {
			g.Skip("No labeled node was found, skipping testing")
		}
		tunedNodeNameList := strings.Fields(tunedNodeNames)
		if len(tunedNodeNameList) > 1 {
			utils.Logf("Warning: multiple nodes matched label node-role.kubernetes.io/worker-pao (%v); using first node %q", tunedNodeNames, tunedNodeNameList[0])
		}
		tunedNodeName := tunedNodeNameList[0]

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()
			_ = utils.WaitForMCPUpdate(cleanupCtx, oc, "worker", 600)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("performanceprofile", "pao-baseprofile", "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-pao", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("check If Performance Profile pao-baseprofile and cloud-provider exist during Post Check Phase")
		paoBasePerformanceProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		if !strings.Contains(paoBasePerformanceProfile, "pao-baseprofile") {
			g.Skip("No PerformanceProfile found skipping test")
		}

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("assert if machine config pool applied for worker nodes")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-pao", 1200)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("verify PAO profile was applied correctly")
		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		expectRT := (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64"
		err = utils.VerifyPAOProfile(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile", "pao-baseprofile", expectRT)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check Kernel boot settings passed into /proc/cmdline in labeled node")
		kernelCMDLineStdout, err := utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "cat", "/proc/cmdline")
		utils.Logf("the settings of Kernel boot passed into /proc/cmdline  on labeled nodes: \n%v", kernelCMDLineStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernelCMDLineStdout).NotTo(o.BeEmpty())
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("tsc=reliable"))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("isolcpus="))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("hugepagesz=1G"))

		// o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("nosmt"))
		//     - nosmt  removed nosmt to improve success rate due to limited cpu cores
		// but manually re-enabled when have enough cpu cores
	})

	// author: liqcui@redhat.com
	g.It("[test_id:21995][OTP]support core functionality before upgrading the OCP cluster [Disruptive][Manual]", oteg.Informing(), func(ctx context.Context) {
		// 21995 is a two-part test.  This is the first part: verify NTO works before upgrade.  Do not clean up any resources created here.
		// Cleanup will be performed by the second part of the test.

		if !utils.ImplStringArrayContains(cloudPlatforms, iaasPlatform) {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test")
		}

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		var providerName string

		g.By("label the node with node-role.kubernetes.io/worker-tuning=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		ntoRes := utils.NtoResource{
			Name:        "tuning-pidmax",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "nto-sysctl-template.yaml"),
			SysctlParam: "kernel.pid_max",
			SysctlValue: "282828",
			Label:       "node-role.kubernetes.io/worker-tuning",
		}

		g.By("create tuning-pidmax profile")
		err = ntoRes.ApplyNTOTunedProfile(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert tuning-pidmax profile applied to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("compare the value kernel.pid_max on labeled node, should be 282828")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.pid_max", "282828")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("get cloud provider name")
		providerName, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(providerName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		providerID, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.spec.providerID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerID).NotTo(o.BeEmpty())
		o.Expect(providerID).To(o.ContainSubstring(providerName))

		g.By("apply cloud-provider profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "cloud-provider-profile.yaml"), "-p", "PROVIDER_NAME="+providerName)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check provider + providerName profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("provider-" + providerName))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value is 16386")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "16386")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:21995][OTP]support core functionality after upgrading the OCP cluster [Disruptive][Manual]", oteg.Informing(), func(ctx context.Context) {
		// 21995 is a two-part test.  This is the second part: verify PAO+NTO survives upgrade.  Clean-up any resources created
		// by the first part of the test.

		if !utils.ImplStringArrayContains(cloudPlatforms, iaasPlatform) {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test")
		}

		tunedNodeNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker-tuning", "-ojsonpath={.items[*].metadata.name}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if len(tunedNodeNames) == 0 {
			g.Skip("No suitable worker node was found in : " + iaasPlatform + " - skipping test")
		}
		tunedNodeNameList := strings.Fields(tunedNodeNames)
		if len(tunedNodeNameList) > 1 {
			utils.Logf("Warning: multiple nodes matched label node-role.kubernetes.io/worker-tuning (%v); using first node %q", tunedNodeNames, tunedNodeNameList[0])
		}
		tunedNodeName := tunedNodeNameList[0]

		var providerName string
		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax", "-n", ntoNamespace, "--ignore-not-found").Execute()
			if providerName != "" {
				_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-"+providerName, "-n", ntoNamespace, "--ignore-not-found").Execute()
			}
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("get cloud provider name")
		providerName, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(providerName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert tuning-pidmax profile applied to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("compare if the value kernel.pid_max on labeled node, should be 282828")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.pid_max", "282828")
		o.Expect(err).NotTo(o.HaveOccurred())

		providerID, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.spec.providerID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerID).NotTo(o.BeEmpty())
		o.Expect(providerID).To(o.ContainSubstring(providerName))

		g.By("apply cloud-provider profile")
		err = utils.ApplyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", fx.file("nto", "cloud-provider-profile.yaml"), "-p", "PROVIDER_NAME="+providerName)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check provider + providerName profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("provider-" + providerName))

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value is 16386")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "16386")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:74507][OTP]not log a same-priority warning for non-matching custom profiles with the same priority [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		var firstNodeName string
		var secondNodeName string

		SkipIsSNO(oc)

		totalLinuxWorkerNode, err := utils.CountLinuxWorkerNodes(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		totalLinuxWorkerNodes := strconv.Itoa(totalLinuxWorkerNode)
		if totalLinuxWorkerNode < 2 {
			g.Skip("This test needs at least two worker nodes, have " + totalLinuxWorkerNodes + ". skipping test.")
		}

		firstNodeName, _, err = utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())
		secondNodeName, _, err = utils.GetLinuxWorkerNode(oc, 1)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", firstNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondNodeName, "node-role.kubernetes.io/worker-priority18-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax2", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get the tuned pod name in the same node that labeled node
		ntoOperatorPodName, err := utils.GetNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ntoOperatorPodName).NotTo(o.BeEmpty())

		g.By("pick two worker nodes to label as worker-tuning and worker-priority18")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", firstNodeName, "node-role.kubernetes.io/worker-tuning=").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondNodeName, "node-role.kubernetes.io/worker-priority18=").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		firstNTORes := utils.NtoResource{
			Name:        "tuning-pidmax",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "nto-sysctl-template.yaml"),
			SysctlParam: "kernel.pid_max",
			SysctlValue: "282828",
			Label:       "node-role.kubernetes.io/worker-tuning",
		}

		// Setting "vm.dirty_ratio" via sysctl in tuned is deprecated now; using kernel.pid_max for the second node to make things simple.
		secondNTORes := utils.NtoResource{
			Name:        "tuning-pidmax2",
			Namespace:   ntoNamespace,
			Template:    fx.file("nto", "nto-sysctl-template.yaml"),
			SysctlParam: "kernel.pid_max",
			SysctlValue: "2097152",
			Label:       "node-role.kubernetes.io/worker-priority18",
		}

		g.By("create tuning-pidmax profile")
		err = firstNTORes.ApplyNTOTunedProfile(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create tuning-pidmax2 profile")
		err = secondNTORes.ApplyNTOTunedProfile(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create tuning-pidmax profile and apply it to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, firstNodeName, "tuning-pidmax", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create tuning-pidmax2 profile and apply it to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, secondNodeName, "tuning-pidmax2", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By(fmt.Sprintf("check the value kernel.pid_max on %v is 282828", firstNodeName))
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, firstNodeName, "kernel.pid_max", "282828")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By(fmt.Sprintf("check the value kernel.pid_max on %v is 2097152", secondNodeName))
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, secondNodeName, "kernel.pid_max", "2097152")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("make sure operator pod logs do not contain 'same priority' substring")
		ntoOperatorPodLogs, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, ntoOperatorPodName, "--tail=50").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ntoOperatorPodLogs).NotTo(o.BeEmpty())
		o.Expect(ntoOperatorPodLogs).NotTo(o.ContainSubstring("same priority"))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:75555][OTP]start the tuned pod before workload pods on node reboot [Disruptive][Slow]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		tunedNodeName, pool, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		initialMachineCount, err := utils.GetPoolUpdatedMachineCount(ctx, oc, pool)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()
			_ = utils.WaitForPoolUpdatedMachineCount(cleanupCtx, oc, pool, initialMachineCount)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("performanceprofile", "pao-baseprofile", "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-pao", "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get how many cpus on the specified worker node
		g.By("get the number of CPU cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node does not have enough cpus - skipping test")
		}
		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("label the node with node-role.kubernetes.io/worker-pao=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			// Only GCP and AWS support realtime-kernel
			g.By("apply pao-baseprofile performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile.yaml"), "-p", "ISENABLED=true")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else if ocpArch == "ppc64le" {
			g.By("apply pao-baseprofile performance profile for ppc64le")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile-ppc64le.yaml"), "-p", "ISENABLED=false")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			g.By("apply pao-baseprofile performance profile")
			err = utils.ApplyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", fx.file("pao", "pao-baseprofile.yaml"), "-p", "ISENABLED=false")
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		g.By("check Performance Profile pao-baseprofile was created automatically")
		paoBasePerformanceProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(paoBasePerformanceProfile).NotTo(o.BeEmpty())
		o.Expect(paoBasePerformanceProfile).To(o.ContainSubstring("pao-baseprofile"))

		g.By("create machine config pool worker-pao")
		err = utils.CreateOperatorResourceByYaml(oc, "", fx.file("pao", "pao-baseprofile-mcp.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if machine config pool applied for worker nodes")
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-pao", 1200)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("verify PAO profile was applied correctly")
		expectRT := (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64"
		err = utils.VerifyPAOProfile(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile", "pao-baseprofile", expectRT)
		o.Expect(err).NotTo(o.HaveOccurred())

		// $ systemctl status  ocp-tuned-one-shot.service
		// ocp-tuned-one-shot.service - TuneD service from NTO image
		// ..
		// Active: inactive (dead) since Thu 2024-06-20 14:29:32 UTC; 5min ago
		// notice the one-shot tuned service started and finished before kubelet
		// Return an error when the systemctl status ocp-tuned-one-shot.service is inactive, so err for o.Expect as expected.
		g.By("check if end time of ocp-tuned-one-shot.service prior to startup time of kubelet service")

		// supported property name
		// 0.InactiveExitTimestampMonotonic
		// 1.ExecMainStartTimestampMonotonic
		// 2.ActiveEnterTimestampMonotonic
		// 3.StateChangeTimestampMonotonic
		// 4.ActiveExitTimestampMonotonic
		// 5.InactiveEnterTimestampMonotonic
		// 6.ConditionTimestampMonotonic
		// 7.AssertTimestampMonotonic
		inactiveExitTimestampMonotonicOfOCPTunedOneShotService, err := utils.ShowSystemctlPropertyValueOfServiceUnitByName(oc, tunedNodeName, ntoNamespace, "ocp-tuned-one-shot.service", "InactiveExitTimestampMonotonic")
		o.Expect(err).NotTo(o.HaveOccurred())
		ocpTunedOneShotServiceStatusInactiveExitTimestamp, err := utils.GetSystemctlServiceUnitTimestampByPropertyNameWithMonotonic(inactiveExitTimestampMonotonicOfOCPTunedOneShotService)
		o.Expect(err).NotTo(o.HaveOccurred())

		execMainStartTimestampMonotonicOfKubelet, err := utils.ShowSystemctlPropertyValueOfServiceUnitByName(oc, tunedNodeName, ntoNamespace, "kubelet.service", "ExecMainStartTimestampMonotonic")
		o.Expect(err).NotTo(o.HaveOccurred())
		kubeletServiceStatusExecMainStartTimestamp, err := utils.GetSystemctlServiceUnitTimestampByPropertyNameWithMonotonic(execMainStartTimestampMonotonicOfKubelet)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("ocpTunedOneShotServiceStatusInactiveExitTimestamp is: %v, kubeletServiceStatusActiveEnterTimestamp is: %v", ocpTunedOneShotServiceStatusInactiveExitTimestamp, kubeletServiceStatusExecMainStartTimestamp)

		o.Expect(kubeletServiceStatusExecMainStartTimestamp).To(o.BeNumerically(">", ocpTunedOneShotServiceStatusInactiveExitTimestamp))
	})

	// author: liqcui@redhat.com
	g.It("[test_id:75435][OTP]defer profile updates until node reboot when the deferred annotation is set to update [Disruptive]", oteg.Informing(), g.NodeTimeout(15*time.Minute), func(ctx context.Context) {
		SkipIsSNO(oc)

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-update-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "deferred-update-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
			if tunedPodName != "" {
				// This test will make other tests (e.g. 29789) fail, because it will not restart tuned.  This is a problem if tuned-main.conf
				// file is changed because of configuration parameters such as 'reapply_sysctl' or 'debug'.  Ensure we restart tuned once we finish.
				_ = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", ntoNamespace, tunedPodName, "--", "pkill", "-F", "/run/tuned/tuned.pid").Execute()
			}
			_ = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(cleanupCtx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-update=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		deferredNTORes := utils.NtoResource{
			Name:          "deferred-update-profile",
			Namespace:     ntoNamespace,
			Template:      fx.file("nto", "deferred-nto.yaml"),
			SysctlParam:   "kernel.shmmni",
			SysctlValue:   "8192",
			Label:         "deferred-update",
			DeferredValue: "update",
		}

		g.By("create deferred-update profile")
		err = deferredNTORes.ApplyNTOTunedProfileWithDeferredAnnotation(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create deferred-update profile and apply it to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "deferred-update-profile", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		g.By("compare the value kernel.shmmni on labeled node, should be 8192")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "8192")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("patch tuned with new value of kernel.shmmni to 10240")
		err = utils.PatchTunedProfile(oc, ntoNamespace, "deferred-update-profile", fx.file("nto", "deferred-nto-update-patch.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("patch the tuned profile with a new value, the new value takes effect after node reboot")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "deferred-update-profile", "False")
		o.Expect(err).NotTo(o.HaveOccurred())

		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.conditions[0].message}`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(output).To(o.ContainSubstring("The TuneD daemon profile is waiting for the next node restart"))

		g.By("reboot the node with updated tuned profile")
		err = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", ntoNamespace, "-it", tunedPodName, "--", "reboot").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.WaitForTunedProfileAppliedWithTimeout(ctx, oc, ntoNamespace, tunedNodeName, "deferred-update-profile", 600*time.Second, "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("compare the value kernel.shmmni on labeled node, should be 10240")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "10240")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("removed deferred tuned custom profile and unlabel node")
		err = deferredNTORes.Delete(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("compare the value kernel.shmmni on labeled node, it will roll back to 4096")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: sahshah
	g.It("[test_id:75434][OTP]defer all profile changes until node reboot when the deferred annotation is set to always [Disruptive]", oteg.Informing(), g.NodeTimeout(15*time.Minute), func(ctx context.Context) {
		SkipIsSNO(oc)

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-always-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "deferred-always-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
			if tunedPodName != "" {
				// This test will make other tests (e.g. 29789) fail, because it will not restart tuned.  This is a problem if tuned-main.conf
				// file is changed because of configuration parameters such as 'reapply_sysctl' or 'debug'.  Ensure we restart tuned once we finish.
				_ = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", ntoNamespace, tunedPodName, "--", "pkill", "-F", "/run/tuned/tuned.pid").Execute()
			}
			_ = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(cleanupCtx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("pick one worker node to label to deferred-always")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-always=").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		deferredNTORes := utils.NtoResource{
			Name:          "deferred-always-profile",
			Namespace:     ntoNamespace,
			Template:      fx.file("nto", "deferred-nto.yaml"),
			SysctlParam:   "kernel.shmmni",
			SysctlValue:   "8192",
			Label:         "deferred-always",
			DeferredValue: "always",
		}

		g.By("create deferred-always profile")
		err = deferredNTORes.ApplyNTOTunedProfileWithDeferredAnnotation(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create deferred-always profile and apply it to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "openshift-node", "False")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.conditions[0].message}`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(output).To(o.ContainSubstring("The TuneD daemon profile is waiting for the next node restart"))

		g.By("compare the value kernel.shmmni on labeled node, should be 4096")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("reboot the node with updated tuned profile")
		err = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", ntoNamespace, "-it", tunedPodName, "--", "reboot").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.WaitForTunedProfileAppliedWithTimeout(ctx, oc, ntoNamespace, tunedNodeName, "deferred-always-profile", 600*time.Second, "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the value kernel.shmmni on labeled node, should be 8192")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "8192")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("removed deferred tuned custom profile and unlabel node")
		err = deferredNTORes.Delete(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the value kernel.shmmni on labeled node, it will roll back to 4096")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:77764][OTP]allow kubelet to start when the NTO image cannot be pulled [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		proxyStdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("proxy", "cluster", "-ojsonpath={.spec.httpsProxy}").Output()
		utils.Logf("proxyStdOut is %v", proxyStdOut)
		o.Expect(err).NotTo(o.HaveOccurred())
		if len(proxyStdOut) == 0 {
			g.Skip("No proxy in the cluster - skipping test")
		}
		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		// numaNode = 0 hard coded in NTO requires a switch to a custom yaml
		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		disableHttpsPPFile := fx.file("nto", "disable-https-pp.yaml")
		if ocpArch == "ppc64le" {
			disableHttpsPPFile = fx.file("nto", "disable-https-pp-ppc64le.yaml")
		}

		// Get how many cpus on the specified worker node
		g.By("get the number of CPU cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())

		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node does not have enough cpus - skipping test")
		}

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-nohttps-").Execute()
			_ = utils.WaitForMCPUpdate(cleanupCtx, oc, "worker", 720)
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-nohttps", "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("PerformanceProfile", "performance", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("label the node with node-role.kubernetes.io/worker-nohttps=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-nohttps=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "disable-https-mcp.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("remove NTO image on label node")
		stdOut, err := utils.DebugNodeRetryWithOptionsAndChroot(ctx, oc, tunedNodeName, []string{"-q"}, 3*time.Minute, "/bin/bash", "-c", ". /var/lib/ocp-tuned/image.env;podman rmi $NTO_IMAGE --force")
		utils.Logf("removed NTO image is %v", stdOut)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("apply pao performance profile")
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, disableHttpsPPFile)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.WaitForMCPUpdate(ctx, oc, "worker-nohttps", 720)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		// Inactive status mean error in systemctl status ocp-tuned-one-shot.service, that's expected
		g.By("check systemctl status ocp-tuned-one-shot.service, Active: inactive is expected")
		stdOut, _ = utils.DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "systemctl", "status", "ocp-tuned-one-shot.service")
		o.Expect(stdOut).To(o.ContainSubstring("ocp-tuned-one-shot.service: Deactivated successfully"))

		g.By("check systemctl status kubelet, Active: active (running) is expected")
		stdOut, err = utils.DebugNodeRetryWithOptionsAndChroot(ctx, oc, tunedNodeName, []string{"-q"}, 3*time.Minute, "systemctl", "status", "kubelet")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stdOut).To(o.ContainSubstring("Active: active (running)"))

		g.By("remove NTO image on label node and delete tuned pod, the image can pull successfully")
		stdOut, err = utils.DebugNodeRetryWithOptionsAndChroot(ctx, oc, tunedNodeName, []string{"-q"}, 3*time.Minute, "/bin/bash", "-c", ". /var/lib/ocp-tuned/image.env;podman rmi $NTO_IMAGE --force")
		utils.Logf("removed NTO image is %v", stdOut)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "pod", tunedPodName).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node again
		tunedPodName, err = utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.AssertPodToBeReady(ctx, oc, tunedPodName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		podDescOutput, err := oc.AsAdmin().WithoutNamespace().Run("describe").Args("-n", ntoNamespace, "pod", tunedPodName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podDescOutput).To(o.ContainSubstring("Successfully pulled image"))
	})

	// author: sahshah
	g.It("[test_id:76674][OTP]apply profile changes immediately when the deferred annotation is set to never [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-never-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "deferred-never-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(cleanupCtx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("label the node with deferred-never=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-never=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		deferredNTORes := utils.NtoResource{
			Name:          "deferred-never-profile",
			Namespace:     ntoNamespace,
			Template:      fx.file("nto", "deferred-nto.yaml"),
			SysctlParam:   "kernel.shmmni",
			SysctlValue:   "8192",
			Label:         "deferred-never",
			DeferredValue: "never",
		}

		g.By("compare the value kernel.shmmni on labeled node, should be 4096")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create deferred-never profile")
		err = deferredNTORes.ApplyNTOTunedProfileWithDeferredAnnotation(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create deferred-never profile and apply it to nodes")
		err = utils.WaitForTunedProfileApplied(ctx, oc, ntoNamespace, tunedNodeName, "deferred-never-profile", "True")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		utils.LogCurrentProfiles(oc, ntoNamespace)

		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.conditions[0].message}`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(output).To(o.ContainSubstring("TuneD profile applied"))

		g.By("compare the value kernel.shmmni on labeled node, should be 8192")
		err = utils.CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx, oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "8192")
		o.Expect(err).NotTo(o.HaveOccurred())
	})

	// author: liqcui@redhat.com
	g.It("[test_id:80233][OTP]detect and report duplicate TuneD profiles with conflicting content [Disruptive]", oteg.Informing(), func(ctx context.Context) {
		SkipIsSNO(oc)

		tunedNodeName, _, err := utils.GetLinuxWorkerNode(oc, 0)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.DeferCleanup(func(cleanupCtx context.Context) {
			_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-dup-").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-profile-dup1", "-n", ntoNamespace, "--ignore-not-found").Execute()
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-profile-dup2", "-n", ntoNamespace, "--ignore-not-found").Execute()
			utils.WaitForDefaultProfiles(cleanupCtx, oc, ntoNamespace)
		})

		g.By("label the node with node-role.kubernetes.io/worker-dup=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-dup=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// Get the tuned pod name in the same node that labeled node
		tunedPodName, err := utils.GetTunedPodNameByNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		ntoOperatorPod, err := utils.GetNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("the tuned operator pod name is: \n%v", ntoOperatorPod)

		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "nto-same-profile-diff-content1.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())
		err = utils.CreateOperatorResourceByYaml(oc, ntoNamespace, fx.file("nto", "nto-same-profile-diff-content2.yaml"))
		o.Expect(err).NotTo(o.HaveOccurred())

		// The conflicting duplicate profile "openshift-profile-dup" is intentionally
		// never applied to the node.  Wait for the operator to report the conflict
		// in the Tuned CR status before asserting on it.
		g.By("wait for the operator to detect and report the conflicting duplicate TuneD profiles")
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, false, func(_ context.Context) (bool, error) {
			customizedTunedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned/openshift-profile-dup1", "-ojsonpath={.status}").Output()
			if err != nil {
				utils.Logf("failed to get status of tuned/openshift-profile-dup1: %v", err)
				return false, nil
			}
			return strings.Contains(customizedTunedStatus, "Duplicate TuneD profile") && strings.Contains(customizedTunedStatus, "conflicting content"), nil
		})
		o.Expect(err).NotTo(o.HaveOccurred())

		TunedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("current tuned list: \n%v", TunedStatus)

		ProfileStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		utils.Logf("current profile of each nodes: \n%v", ProfileStatus)

		// The status show Duplicate TuneD profile \"openshift-profile\" with conflicting content
		customizedTunedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned/openshift-profile-dup1", "-ojsonpath={.status}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(customizedTunedStatus).To(o.And(
			o.ContainSubstring("Duplicate TuneD profile"),
			o.ContainSubstring("conflicting content")))

		customizedTunedStatus, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned/openshift-profile-dup2", "-ojsonpath={.status}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(customizedTunedStatus).To(o.And(
			o.ContainSubstring("Duplicate TuneD profile"),
			o.ContainSubstring("conflicting content")))

		err = utils.AssertNTOPodLogsLastLines(ctx, oc, ntoNamespace, ntoOperatorPod, "15", 60, "duplicate TuneD profile openshift-profile-dup with conflicting content detected")
		o.Expect(err).NotTo(o.HaveOccurred())
	})
})
