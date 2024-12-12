package utils

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
)

// RoleWorkerCNF contains role name of cnf worker nodes
var RoleWorkerCNF string

// NodeSelectorLabels contains the node labels the performance profile should match
var NodeSelectorLabels map[string]string

// PerformanceProfileName contains the name of the PerformanceProfile created for tests
// or an existing profile when discover mode is enabled
var PerformanceProfileName string

// NodesSelector represents the label selector used to filter impacted nodes.
var NodesSelector string

// ProfileNotFound is true when discovery mode is enabled and no valid profile was found
var ProfileNotFound bool

// NtoImage represents NTO Image location which is either quay.io or any other internal registry
var NTOImage string

// MustGatherDir represents Mustgather directory created using oc adm mustgather
var MustGatherDir string

func init() {
	RoleWorkerCNF = os.Getenv("ROLE_WORKER_CNF")
	if RoleWorkerCNF == "" {
		RoleWorkerCNF = "worker-cnf"
	}

	PerformanceProfileName = os.Getenv("PERF_TEST_PROFILE")
	if PerformanceProfileName == "" {
		PerformanceProfileName = "performance"
	}

	NodesSelector = os.Getenv("NODES_SELECTOR")

	if !hypershift.IsHypershiftCluster() {
		NodeSelectorLabels = map[string]string{
			fmt.Sprintf("%s/%s", LabelRole, RoleWorkerCNF): "",
		}
	} else {
		NodeSelectorLabels = map[string]string{
			fmt.Sprintf("%s/%s", LabelRole, RoleWorker): "",
		}
	}

	NTOImage = os.Getenv("NTO_IMAGE")

	if NTOImage == "" {
		NTOImage = "quay.io/openshift/origin-cluster-node-tuning-operator:latest"
	}

	MustGatherDir = os.Getenv("MUSTGATHER_DIR")

	if discovery.Enabled() {
		profile, err := discovery.GetDiscoveryPerformanceProfile(NodesSelector)
		if err == discovery.ErrProfileNotFound {
			ProfileNotFound = true
			return
		}

		if err != nil {
			fmt.Println("Failed to find profile in discovery mode", err)
			ProfileNotFound = true
			return
		}

		PerformanceProfileName = profile.Name

		NodeSelectorLabels = profile.Spec.NodeSelector
		if NodesSelector != "" {
			keyValue := strings.Split(NodesSelector, "=")
			if len(keyValue) == 1 {
				keyValue = append(keyValue, "")
			}
			NodeSelectorLabels[keyValue[0]] = keyValue[1]
		}
	}
}

const (
	// RoleWorker contains the worker role
	RoleWorker = "worker"
	// RoleMaster contains the master role
	RoleMaster = "master"
)

const (
	// LabelRole contains the key for the role label
	LabelRole = "node-role.kubernetes.io"
	// LabelHostname contains the key for the hostname label
	LabelHostname = "kubernetes.io/hostname"
)

const (
	// NamespaceMachineConfigOperator contains the namespace of the machine-config-opereator
	NamespaceMachineConfigOperator = "openshift-machine-config-operator"
	// NamespaceTesting contains the name of the testing namespace
	NamespaceTesting = "performance-addon-operators-testing"
)
const (
	// NodeInspectorName contains the name of node inspector name
	NodeInspectorName = "node-inspector"
	// NodeInspectorNamespace contains the name of node inspector namespace
	NodeInspectorNamespace = "node-inspector-ns"
)

const (
	// FilePathKubeletConfig contains the kubelet.conf file path
	FilePathKubeletConfig = "/etc/kubernetes/kubelet.conf"
)

const (
	// ContainerMachineConfigDaemon contains the name of the machine-config-daemon container
	ContainerMachineConfigDaemon = "machine-config-daemon"
)

const (
	// LogsFetchDuration represents how much in the past we need to go when fetching the pod logs
	LogsFetchDuration = 10 * time.Minute
)
