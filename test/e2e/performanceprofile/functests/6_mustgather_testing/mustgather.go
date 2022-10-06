package pao_mustgather

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	mustGatherPath = "../../testdata/must-gather/must-gather.e2e"
)

var mustGatherContentDir string
var snapshotDir string
var snapshotName string

var _ = Describe("[rfe_id: 50649] Performance Addon Operator Must Gather", func() {
	var profile *performancev2.PerformanceProfile

	testutils.BeforeAll(func() {
		mustgatherPathContent, err := ioutil.ReadDir(mustGatherPath)
		Expect(err).ToNot(HaveOccurred(), "failed to read the must-gather Directory %s: %v", mustGatherPath, err)

		for _, directory := range mustgatherPathContent {
			if strings.Contains(directory.Name(), "must-gather") {
				mustGatherContentDir = filepath.Join(mustGatherPath, directory.Name())
				break
			}
		}

		// pre generated must-gather data
		// hardcored node name
		snapshotDir = filepath.Join(mustGatherContentDir, "nodes", "ci-ln-t0prq3t-72292-h4x2n-worker-a-wqnqt")
		snapshotName = filepath.Join(snapshotDir, "sysinfo.tgz")

		if _, err := os.Stat(snapshotName); errors.Is(err, os.ErrNotExist) {
			Expect(err).ToNot(HaveOccurred(), "failed to read sysinfo.tgz file %s: %v", snapshotName, err)
		}

		err = Untar(snapshotDir, snapshotName)
		Expect(err).ToNot(HaveOccurred(), "failed to read the %s: %v", snapshotName, err)
	})
	Context("PAO must-gather Tests", func() {
		It("Verify Generic cluster resource definitions are captured", func() {
			var genericFiles = []string{
				"version",
				"cluster-scoped-resources/config.openshift.io/featuregates/cluster.yaml",
				"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/default.yaml",
				"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/rendered.yaml",
			}
			err := checkfilesExist(genericFiles, mustGatherContentDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Verify PAO cluster resources are captured", func() {
			profile, _ = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			if profile == nil {
				Skip("No Performance Profile found")
			}
			//replace peformance.yaml for profile.Name when data is generated in the node
			ClusterSpecificFiles := []string{
				"cluster-scoped-resources/performance.openshift.io/performanceprofiles/performance.yaml",
				"cluster-scoped-resources/machineconfiguration.openshift.io/kubeletconfigs/performance-performance.yaml",
				"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/openshift-node-performance-performance.yaml",
			}
			err := checkfilesExist(ClusterSpecificFiles, mustGatherContentDir)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Verify hardware related information are captured", func() {
			nodeSpecificFiles := []string{
				"sys/devices/system/cpu/offline",
				"proc/cpuinfo",
				"cpu_affinities.json",
				"dmesg",
				"irq_affinities.json",
				"lscpu",
				"machineinfo.json",
				"podresources.json",
				"proc_cmdline",
				"sysinfo.log",
			}
			err := checkfilesExist(nodeSpecificFiles, snapshotDir)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Verify machineconfig resources are captured", func() {
			mcps := &machineconfigv1.MachineConfigPoolList{}
			err := testclient.Client.List(context.TODO(), mcps)
			Expect(err).ToNot(HaveOccurred())
			mcpFiles := make([]string, len(mcps.Items))
			for _, item := range mcps.Items {
				mcpFiles = append(mcpFiles, fmt.Sprintf("cluster-scoped-resources/machineconfiguration.openshift.io/machineconfigpools/%s.yaml", item.Name))
			}
			err = checkfilesExist(mcpFiles, mustGatherContentDir)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

func checkfilesExist(listOfFiles []string, path string) error {
	for _, f := range listOfFiles {
		file := filepath.Join(path, f)
		if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func Untar(root string, snapshotName string) error {
	var err error
	r, err := os.Open(snapshotName)
	if err != nil {
		return err
	}
	defer r.Close()

	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return err
		}

		target := filepath.Join(root, header.Name)
		mode := os.FileMode(header.Mode)

		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, mode)
			if err != nil {
				return err
			}

		case tar.TypeReg:
			dst, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, mode)
			if err != nil {
				return err
			}

			_, err = io.Copy(dst, tr)
			if err != nil {
				return err
			}

			dst.Close()
		}
	}
}
