package pao_mustgather

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	hypershiftutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jaypipes/ghw/pkg/snapshot"
)

const destDir = "must-gather"

var _ = Describe("[rfe_id: 50649] Performance Addon Operator Must Gather", Label(string(label.MustGather)), func() {
	mgContentFolder := ""

	testutils.CustomBeforeAll(func() {
		destDirContent, err := os.ReadDir(destDir)
		Expect(err).NotTo(HaveOccurred(), "unable to read contents from destDir:%s. error: %w", destDir, err)

		for _, content := range destDirContent {
			if !content.IsDir() {
				continue
			}
			mgContentFolder = filepath.Join(destDir, content.Name())
		}
	})

	Context("with a freshly executed must-gather command", Label(string(label.Tier1)), func() {
		It("Verify Generic cluster resource definitions are captured", func() {

			var genericFiles = []string{
				"version",
				"cluster-scoped-resources/config.openshift.io/featuregates/cluster.yaml",
				"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/default.yaml",
			}

			By(fmt.Sprintf("Checking Folder: %q\n", mgContentFolder))
			By("Looking for generic files")
			err := checkfilesExist(genericFiles, mgContentFolder)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Verify PAO cluster resources are captured", func() {
			profile, _ := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			if profile == nil {
				Skip("No Performance Profile found")
			}
			//replace peformance.yaml for profile.Name when data is generated in the node
			ClusterSpecificFiles := []string{
				"cluster-scoped-resources/performance.openshift.io/performanceprofiles/performance.yaml",
				"cluster-scoped-resources/machineconfiguration.openshift.io/kubeletconfigs/performance-performance.yaml",
				"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/openshift-node-performance-performance.yaml",
			}
			// On a hypershift env, the tuned file name has an indentifier in the end
			if hypershiftutils.IsHypershiftCluster() {
				ClusterSpecificFiles = []string{
					"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/openshift-node-performance-performance-*.yaml",
				}
			}
			By(fmt.Sprintf("Checking Folder: %q\n", mgContentFolder))
			By("Looking for generic files")
			err := checkfilesExist(ClusterSpecificFiles, mgContentFolder)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Verify hardware related information are captured", func() {

			var workerRTNodes []corev1.Node

			workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
			Expect(err).ToNot(HaveOccurred())
			cnfWorkerNode := workerRTNodes[0].Name

			// find the path of sysinfo.tgz of the correct node
			snapShotName := ""
			err = filepath.Walk(mgContentFolder,
				func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if !info.IsDir() && info.Name() == "sysinfo.tgz" {
						if strings.Contains(path, cnfWorkerNode) {
							snapShotName = path
						}
					}
					return nil
				})
			if err != nil {
				log.Println(err)
			}

			// Two different folders for must-gather info, first one with generated file and second one tmp folder with unzip info from sysinfo.tgz
			// find the path of must-gather node files
			snapShotName = ""
			err = filepath.Walk(mgContentFolder,
				func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if !info.IsDir() && info.Name() == "sysinfo.tgz" {
						if strings.Contains(path, cnfWorkerNode) {
							snapShotName = path
						}
					}
					return nil
				})
			if err != nil {
				log.Println(err)
			}
			snapShotPath := filepath.Dir(snapShotName)

			nodeSpecificFiles := []string{
				"cpu_affinities.json",
				"dmesg",
				"irq_affinities.json",
				"lscpu",
				"podresources.json",
				"proc_cmdline",
				"sysinfo.log",
			}

			err = checkfilesExist(nodeSpecificFiles, snapShotPath)
			Expect(err).ToNot(HaveOccurred())

			// Check files of sysinfo.tgz
			snapShotDir, err := snapshot.Unpack(snapShotName)
			Expect(err).ToNot(HaveOccurred(), "failed to read the %s: %v", snapShotName, err)

			nodeSpecificFiles = []string{
				"sys/devices/system/cpu/offline",
				"proc/cpuinfo",
				"machineinfo.json",
			}

			err = checkfilesExist(nodeSpecificFiles, snapShotDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Verify machineconfig resources are captured", Label(string(label.OpenShift)), func() {
			mcps := &machineconfigv1.MachineConfigPoolList{}
			err := testclient.ControlPlaneClient.List(context.TODO(), mcps)
			Expect(err).ToNot(HaveOccurred())
			mcpFiles := make([]string, len(mcps.Items))
			for _, item := range mcps.Items {
				mcpFiles = append(mcpFiles, fmt.Sprintf("cluster-scoped-resources/machineconfiguration.openshift.io/machineconfigpools/%s.yaml", item.Name))
			}
			err = checkfilesExist(mcpFiles, mgContentFolder)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

func checkfilesExist(listOfFiles []string, path string) error {
	for _, f := range listOfFiles {
		file := filepath.Join(path, f)
		pattern, err := filepath.Glob(file)
		if err != nil {
			// if the pattern itself is malformed.
			return fmt.Errorf("failed to match pattern for file: %s, error: %w", file, err)
		}
		if pattern == nil {
			// When no files matched the pattern.
			return fmt.Errorf("no files matched the pattern for file: %s, error: %w", file, err)
		}
		if _, err = os.Stat(pattern[0]); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("file does not exist: %s, error: %w", pattern[0], err)
			}
			// Return the original error if it's not os.ErrNotExist.
			return fmt.Errorf("failed to stat file: %s, error: %w", pattern[0], err)
		}
	}
	return nil
}
