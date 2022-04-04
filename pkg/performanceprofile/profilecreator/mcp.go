package profilecreator

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

// GetMCPSelector returns a label that is unique to the target pool, error otherwise
func GetMCPSelector(pool *mcfgv1.MachineConfigPool, clusterPools []*mcfgv1.MachineConfigPool) (map[string]string, error) {
	mcpSelector := make(map[string]string)

	// go over all the labels to find the unique ones
	for key, value := range pool.Labels {
		unique := true
		for _, mcp := range clusterPools {
			if mcp.Name == pool.Name {
				continue
			}
			if mcpValue, found := mcp.Labels[key]; found {
				if value == mcpValue {
					unique = false
					break
				}
			}
		}
		if unique {
			mcpSelector[key] = value
		}
	}

	if len(mcpSelector) == 0 {
		return nil, fmt.Errorf("can't find a unique label for '%s' MCP", pool.Name)
	}

	// find a label that includes the MCP name
	if len(mcpSelector) > 1 {
		for key, value := range mcpSelector {
			if strings.HasSuffix(key, pool.Name) {
				mcpSelector = make(map[string]string)
				mcpSelector[key] = value
				break
			}
		}
	}

	// pick a single unique label
	if len(mcpSelector) > 1 {
		for key, value := range mcpSelector {
			mcpSelector = make(map[string]string)
			mcpSelector[key] = value
			break
		}
	}

	return mcpSelector, nil
}

// GetNodesForPool returns the nodes belonging to the input mcp
// Adapted (including dependencies) from:
// https://github.com/openshift/machine-config-operator/blob/e4aa3bc5a405c67fb112b24e24b2c372457b3358/pkg/controller/node/node_controller.go#L745
func GetNodesForPool(pool *mcfgv1.MachineConfigPool, clusterPools []*mcfgv1.MachineConfigPool, clusterNodes []*corev1.Node) ([]*corev1.Node, error) {
	var nodes []*corev1.Node

	poolNodeSelector, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid label selector: %v", err)
	}

	for _, n := range clusterNodes {
		p, err := getPrimaryPoolForNode(n, clusterPools)
		if err != nil {
			log.Warningf("can't get pool for node %q: %v", n.Name, err)
			continue
		}
		if p == nil {
			continue
		}
		if p.Name != pool.Name {
			continue
		}
		var unschedulable bool
		for _, taint := range n.Spec.Taints {
			if taint.Effect == corev1.TaintEffectNoSchedule && poolNodeSelector.Matches(labels.Set{taint.Key: taint.Value}) {
				unschedulable = true
				break
			}
		}
		if unschedulable {
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// getPrimaryPoolForNode uses getPoolsForNode and returns the first one which is the one the node targets
func getPrimaryPoolForNode(node *corev1.Node, clusterPools []*mcfgv1.MachineConfigPool) (*mcfgv1.MachineConfigPool, error) {
	pools, err := getPoolsForNode(node, clusterPools)
	if err != nil {
		return nil, err
	}
	if pools == nil {
		return nil, nil
	}
	return pools[0], nil
}

// getPoolsForNode chooses the MachineConfigPools that should be used for a given node.
// It disambiguates in the case where e.g. a node has both master/worker roles applied,
// and where a custom role may be used. It returns a slice of all the pools the node belongs to.
// It also ignores the Windows nodes.
func getPoolsForNode(node *corev1.Node, clusterPools []*mcfgv1.MachineConfigPool) ([]*mcfgv1.MachineConfigPool, error) {
	if isWindows(node) {
		// This is not an error, is this a Windows Node and it won't be managed by MCO. We're explicitly logging
		// here at a high level to disambiguate this from other pools = nil  scenario
		log.Infof("Node %v is a windows node so won't be managed by MCO", node.Name)
		return nil, nil
	}

	var pools []*mcfgv1.MachineConfigPool
	for _, p := range clusterPools {
		selector, err := metav1.LabelSelectorAsSelector(p.Spec.NodeSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid label selector: %v", err)
		}

		// If a pool with a nil or empty selector creeps in, it should match nothing, not everything.
		if selector.Empty() || !selector.Matches(labels.Set(node.Labels)) {
			continue
		}

		pools = append(pools, p)
	}

	if len(pools) == 0 {
		// This is not an error, as there might be nodes in cluster that are not managed by machineconfigpool.
		return nil, nil
	}

	var master, worker *mcfgv1.MachineConfigPool
	var custom []*mcfgv1.MachineConfigPool
	for _, pool := range pools {
		if pool.Name == "master" {
			master = pool
		} else if pool.Name == "worker" {
			worker = pool
		} else {
			custom = append(custom, pool)
		}
	}

	if len(custom) > 1 {
		return nil, fmt.Errorf("node %s belongs to %d custom roles, cannot proceed with this Node", node.Name, len(custom))
	} else if len(custom) == 1 {
		// We don't support making custom pools for masters
		if master != nil {
			return nil, fmt.Errorf("node %s has both master role and custom role %s", node.Name, custom[0].Name)
		}
		// One custom role, let's use its pool
		pls := []*mcfgv1.MachineConfigPool{custom[0]}
		if worker != nil {
			pls = append(pls, worker)
		}
		return pls, nil
	} else if master != nil {
		// In the case where a node is both master/worker, have it live under
		// the master pool. This occurs in CodeReadyContainers and general
		// "single node" deployments, which one may want to do for testing bare
		// metal, etc.
		return []*mcfgv1.MachineConfigPool{master}, nil
	}

	// Otherwise, it's a worker with no custom roles.
	return []*mcfgv1.MachineConfigPool{worker}, nil
}

// isWindows checks if given node is a Windows node or a Linux node
func isWindows(node *corev1.Node) bool {
	windowsOsValue := "windows"
	if value, ok := node.ObjectMeta.Labels["kubernetes.io/os"]; ok {
		if value == windowsOsValue {
			return true
		}
		return false
	}
	// All the nodes should have a OS label populated by kubelet, if not just to maintain
	// backwards compatibility, we can returning true here.
	return false
}
