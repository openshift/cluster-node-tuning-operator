// cluster_helpers.go provides cluster topology detection and node discovery

package utils

import (
	"fmt"
	"os"
	"strings"
)

// ClusterTopology represents the cluster topology type.
type ClusterTopology int

const (
	// TopologyUnknown represents an unknown or unsupported cluster topology.
	TopologyUnknown ClusterTopology = iota
	// TopologySNO represents a Single Node OpenShift cluster (1 node, both master and worker).
	TopologySNO
	// TopologyCompact represents a compact cluster (3 nodes, all are both master and worker).
	TopologyCompact
	// TopologyStandard represents a standard cluster with dedicated master and worker nodes.
	TopologyStandard
)

// countSliceMatches returns the number of elements in b that also appear in a.
func countSliceMatches(a, b []string) int {
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		set[v] = struct{}{}
	}
	count := 0
	for _, v := range b {
		if _, ok := set[v]; ok {
			count++
		}
	}
	return count
}

// Is3MasterNoDedicatedWorkerNode returns true when all three nodes are both master and worker (compact topology).
func Is3MasterNoDedicatedWorkerNode(oc *CLI) bool {
	return GetClusterTopology(oc) == TopologyCompact
}

// GetClusterTopology detects and returns the cluster topology.
func GetClusterTopology(oc *CLI) ClusterTopology {
	masterNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/control-plane=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return TopologyUnknown
	}
	workerNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return TopologyUnknown
	}
	masters := strings.Fields(masterNodes)
	workers := strings.Fields(workerNodes)

	if len(masters) == 1 && len(workers) == 1 && masters[0] == workers[0] {
		return TopologySNO
	}

	if len(masters) == 3 && len(workers) == 3 && countSliceMatches(masters, workers) == 3 {
		return TopologyCompact
	}

	if len(masters) >= 1 && len(workers) >= 1 {
		return TopologyStandard
	}

	return TopologyUnknown
}

// IsSNOOrCompact returns true for SNO or compact (3-master) clusters.
func IsSNOOrCompact(oc *CLI) bool {
	topo := GetClusterTopology(oc)
	return topo == TopologySNO || topo == TopologyCompact
}

// IsSNOCluster returns true when the cluster has one node that is both master and worker.
func IsSNOCluster(oc *CLI) bool {
	return GetClusterTopology(oc) == TopologySNO
}

// IsSingleMasterCluster returns true when the cluster has exactly one control-plane node.
// This is distinct from IsSNOCluster: a non-HA cluster may have a single control-plane
// node alongside dedicated worker nodes, in which case IsSingleMasterCluster is true
// but IsSNOCluster is false. Use this when a test must skip any cluster with only one
// master (e.g. tests that reboot a master node), regardless of worker topology.
func IsSingleMasterCluster(oc *CLI) (bool, error) {
	masterNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/control-plane=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return false, fmt.Errorf("failed to list control-plane nodes: %w", err)
	}
	masters := strings.Fields(masterNodes)
	return len(masters) == 1, nil
}

// GetLinuxWorkerNode returns the n'th Linux node (0-indexed) and its pool name.
// On regular clusters it returns a worker node with pool "worker".
// On SNO and 3-master compact clusters it returns a master node with pool "master".
func GetLinuxWorkerNode(oc *CLI, n int) (string, string, error) {
	masterNodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/control-plane=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", "", err
	}
	masters := strings.Fields(strings.TrimSpace(masterNodesStr))

	workerNodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", "", err
	}
	workers := strings.Fields(strings.TrimSpace(workerNodesStr))

	isSNO := len(masters) == 1 && len(workers) == 1 && masters[0] == workers[0]
	isCompact := Is3MasterNoDedicatedWorkerNode(oc)

	if isSNO || isCompact {
		if n < 0 || n >= len(masters) {
			return "", "", fmt.Errorf("index %d out of range for %d master nodes", n, len(masters))
		}
		return masters[n], "master", nil
	}

	if n < 0 || n >= len(workers) {
		return "", "", fmt.Errorf("index %d out of range for %d worker nodes", n, len(workers))
	}
	return workers[n], "worker", nil
}

// IsHyperShiftHostedCluster returns true when the cluster's control plane topology is External.
func IsHyperShiftHostedCluster(oc *CLI) bool {
	topology, err := oc.WithoutNamespace().AsAdmin().Run("get").Args("infrastructures.config.openshift.io", "cluster", "-o=jsonpath={.status.controlPlaneTopology}").Output()
	if err != nil {
		Logf("IsHyperShiftHostedCluster: failed to get infrastructure topology: %v", err)
		return false
	}
	Logf("IsHyperShiftHostedCluster: topology is %s", topology)
	if topology == "" {
		status, statusErr := oc.WithoutNamespace().AsAdmin().Run("get").Args("infrastructures.config.openshift.io", "cluster", "-o=jsonpath={.status}").Output()
		if statusErr != nil {
			Logf("IsHyperShiftHostedCluster: failed to get cluster status: %v", statusErr)
		}
		Logf("IsHyperShiftHostedCluster: cluster status: %s", status)
		Logf("IsHyperShiftHostedCluster: failure: topology is empty")
		return false
	}
	return topology == "External"
}

// IsROSAHostedCluster returns true when the cluster is a ROSA hosted cluster,
// detected by reading the cluster-type file from the SHARED_DIR environment variable.
func IsROSAHostedCluster(oc *CLI) bool {
	var clusterType string
	sharedDir := os.Getenv("SHARED_DIR")
	if len(sharedDir) != 0 {
		fmt.Fprintln(os.Stderr, "SHARED_DIR was found")
		byteArray, err := os.ReadFile(sharedDir + "/cluster-type")
		if err != nil {
			if !os.IsNotExist(err) {
				Logf("failed to read cluster-type file: %v", err)
			}
			clusterType = ""
		} else {
			clusterType = string(byteArray)
			clusterType = strings.ToLower(clusterType)
		}
	}
	return strings.Contains(clusterType, "rosa")
}

// GetFirstMasterNodeName returns the name of the first master node.
func GetFirstMasterNodeName(oc *CLI) (string, error) {
	masterNodeNamesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/control-plane=", "-oname").Output()
	if err != nil {
		return "", fmt.Errorf("failed to list master nodes: %w", err)
	}
	if masterNodeNamesStr == "" {
		return "", fmt.Errorf("no master nodes found")
	}
	masterNodeNamesArray := strings.Split(masterNodeNamesStr, "\n")

	if len(masterNodeNamesArray) > 0 {
		firstMasterNodeNameArr := strings.Split(masterNodeNamesArray[0], "/")
		if len(firstMasterNodeNameArr) > 1 {
			return firstMasterNodeNameArr[1], nil
		}
	}
	return "", fmt.Errorf("failed to parse master node name from: %q", masterNodeNamesArray[0])
}

// GetDefaultProfileNameOnMaster returns the default tuned profile name on the master node.
func GetDefaultProfileNameOnMaster(oc *CLI, masterNodeName string) (string, error) {
	defaultProfileName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", "openshift-cluster-node-tuning-operator", "profiles.tuned.openshift.io", masterNodeName, "-ojsonpath={.status.tunedProfile}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get tuned profile for node %s: %w", masterNodeName, err)
	}
	if defaultProfileName == "" {
		return "", fmt.Errorf("tuned profile name is empty for node %s", masterNodeName)
	}

	Logf("defaultProfileName is %v on %v ", defaultProfileName, masterNodeName)
	return defaultProfileName, nil
}
