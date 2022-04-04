package utils

import (
	"fmt"
	"os"
	"strings"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
)

// RoleWorkerCNF contains role name of cnf worker nodes
var RoleWorkerCNF string

// NodeSelectorLabels contains the node labels the perfomance profile should match
var NodeSelectorLabels map[string]string

// PerformanceProfileName contains the name of the PerformanceProfile created for tests
// or an existing profile when discover mode is enabled
var PerformanceProfileName string

// NodesSelector represents the label selector used to filter impacted nodes.
var NodesSelector string

// ProfileNotFound is true when discovery mode is enabled and no valid profile was found
var ProfileNotFound bool

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

	NodeSelectorLabels = map[string]string{
		fmt.Sprintf("%s/%s", LabelRole, RoleWorkerCNF): "",
	}

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
	// FilePathKubeletConfig contains the kubelet.conf file path
	FilePathKubeletConfig = "/etc/kubernetes/kubelet.conf"
)

const (
	// ContainerMachineConfigDaemon contains the name of the machine-config-daemon container
	ContainerMachineConfigDaemon = "machine-config-daemon"
)
