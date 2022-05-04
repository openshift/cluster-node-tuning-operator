package pao_mustgather

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var mustGatherPath = os.Getenv("MUSTGATHER_DIR")
var mustGatherFailed bool
var mustGatherContentDir string

var _ = Describe("[rfe_id: 50649] Performance Addon Operator Must Gather", func() {
	var profile *performancev2.PerformanceProfile
	var workerRTNodes []corev1.Node
	var err error
	if mustGatherPath == "" {
		mustGatherFailed = true
	}

	testutils.BeforeAll(func() {
		if mustGatherFailed {
			Skip("No mustgather directory provided, check MUSTGATHER_DIR environment variable")
		} else {
			mustgatherPathContent, err := ioutil.ReadDir(mustGatherPath)
			Expect(err).To(BeNil(), "failed to read the Mustgather Directory %s: %v", mustGatherPath, err)
			for _, file := range mustgatherPathContent {
				if strings.Contains(file.Name(), "registry") {
					mustGatherContentDir = filepath.Join(mustGatherPath, file.Name())
				}
			}
		}

	})
	Context("PAO Mustgather Tests", func() {
		It("[test_id:50650][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Verify Generic cluster resource definitions are captured", func() {
			var genericFiles = []string{
				"version",
				"cluster-scoped-resources/config.openshift.io/featuregates/cluster.yaml",
				"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/default.yaml",
				"namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/rendered.yaml",
			}
			err := checkfilesExist(genericFiles)
			Expect(err).To(BeNil())
		})

		It("[test_id:50651][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Verify PAO cluster resources are captured", func() {
			profile, _ = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			if profile != nil {
				ClusterSpecificFiles := []string{
					fmt.Sprintf("cluster-scoped-resources/performance.openshift.io/performanceprofiles/performance.yaml"),
					fmt.Sprintf("cluster-scoped-resources/machineconfiguration.openshift.io/kubeletconfigs/performance-%s.yaml", profile.Name),
					fmt.Sprintf("namespaces/openshift-cluster-node-tuning-operator/tuned.openshift.io/tuneds/openshift-node-performance-%s.yaml", profile.Name),
				}
				err := checkfilesExist(ClusterSpecificFiles)
				Expect(err).To(BeNil())
			} else {
				Skip("No Performance Profile found")
			}
		})
		It("[test_id:50652][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Verify hardware related information are captured", func() {
			workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
			Expect(err).ToNot(HaveOccurred())
			for _, node := range workerRTNodes {
				nodeSpecificFiles := []string{
					fmt.Sprintf("nodes/%s/%s_logs_kubelet.gz", node.Name, node.Name),
					fmt.Sprintf("nodes/%s/lscpu", node.Name),
					fmt.Sprintf("nodes/%s/lspci", node.Name),
					fmt.Sprintf("nodes/%s/proc_cmdline", node.Name),
				}
				err := checkfilesExist(nodeSpecificFiles)
				Expect(err).To(BeNil())
			}

		})
		It("[test_id:50653][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Verify machineconfig resources are captured", func() {
			mcps := &machineconfigv1.MachineConfigPoolList{}
			selector := labels.NewSelector()
			err := testclient.Client.List(context.TODO(), mcps, &client.ListOptions{LabelSelector: selector})
			Expect(err).ToNot(HaveOccurred())
			mcpFiles := make([]string, len(mcps.Items))
			for _, item := range mcps.Items {
				mcpFiles = append(mcpFiles, fmt.Sprintf("cluster-scoped-resources/machineconfiguration.openshift.io/machineconfigpools/%s.yaml", item.Name))
			}
			err = checkfilesExist(mcpFiles)
			Expect(err).To(BeNil())
		})
	})
})

func checkfilesExist(listOfFiles []string) error {
	for _, f := range listOfFiles {
		file := filepath.Join(mustGatherContentDir, f)
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		Expect(info.Size()).ToNot(BeZero())
	}
	return nil
}
