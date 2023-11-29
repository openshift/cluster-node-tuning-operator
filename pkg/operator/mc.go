package operator

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-node-tuning-operator/pkg/util"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	GeneratedByControllerVersionAnnotationKey string = "tuned.openshift.io/generated-by-controller-version"
	MachineConfigPrefix                       string = "50-nto"
)

// NewMachineConfig exports newMachineConfig for offline rendering
func NewMachineConfig(name string, annotations map[string]string, labels map[string]string, kernelArguments []string) *mcfgv1.MachineConfig {
	return newMachineConfig(name, annotations, labels, kernelArguments)
}

func newMachineConfig(name string, annotations map[string]string, labels map[string]string, kernelArguments []string) *mcfgv1.MachineConfig {
	if labels == nil {
		labels = map[string]string{}
	}
	if annotations == nil {
		annotations = map[string]string{}
	}

	return &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: mcfgv1.MachineConfigSpec{
			KernelArguments: kernelArguments,
		},
	}
}

func getMachineConfigNameForPools(pools []*mcfgv1.MachineConfigPool) string {
	var (
		sb        strings.Builder
		sbPrimary strings.Builder
	)

	sb.WriteString(MachineConfigPrefix)
	for _, pool := range pools {
		if pool == nil {
			continue
		}

		sb.WriteString("-")
		if pool.Name == "master" || pool.Name == "worker" {
			sbPrimary.WriteString(pool.ObjectMeta.Name)
		} else {
			// This is a custom pool; a node can be a member of only one custom pool => return its name.
			sb.WriteString(pool.ObjectMeta.Name)
			return sb.String()
		}
	}
	sb.WriteString(sbPrimary.String())

	return sb.String()
}

// getPoolsForMachineConfigLabels chooses the MachineConfigPools that use MachineConfigs with labels 'mcLabels'.
// Errors are only returned in cases that warrant event reques (e.g. a failure to list k8s objects).
func (pc *ProfileCalculator) getPoolsForMachineConfigLabels(mcLabels map[string]string) ([]*mcfgv1.MachineConfigPool, error) {
	if len(mcLabels) == 0 {
		return nil, nil
	}

	mcpList, err := pc.listers.MachineConfigPools.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	var pools []*mcfgv1.MachineConfigPool
	for _, p := range mcpList {
		selector, err := metav1.LabelSelectorAsSelector(p.Spec.MachineConfigSelector)
		if err != nil {
			klog.Errorf("invalid label selector %s: %v", util.ObjectInfo(selector), err)
			return nil, nil
		}

		// A pool with a nil or empty selector matches nothing.
		if selector.Empty() || !selector.Matches(labels.Set(mcLabels)) {
			continue
		}

		pools = append(pools, p)
	}

	return pools, nil
}

// getPoolsForNode chooses the MachineConfigPools that should be used for a given node.
// It disambiguates in the case where e.g. a node has both master/worker roles applied,
// and where a custom role may be used. It returns a slice of all the pools the node belongs to.
// Errors are only returned in cases that warrant event reques (e.g. a failure to list k8s objects).
func (pc *ProfileCalculator) getPoolsForNode(node *corev1.Node) ([]*mcfgv1.MachineConfigPool, error) {
	pl, err := pc.listers.MachineConfigPools.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	var pools []*mcfgv1.MachineConfigPool
	for _, p := range pl {
		selector, err := metav1.LabelSelectorAsSelector(p.Spec.NodeSelector)
		if err != nil {
			klog.Errorf("invalid label selector %s in MachineConfigPool %s: %v", util.ObjectInfo(selector), p.ObjectMeta.Name, err)
			continue
		}

		// A pool with a nil or empty selector matches nothing.
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
		klog.Errorf("node %s belongs to %d custom roles, cannot proceed with this Node", node.Name, len(custom))
		return nil, nil
	} else if len(custom) == 1 {
		// We don't support making custom pools for masters
		if master != nil {
			klog.Errorf("node %s has both master role and custom role %s", node.Name, custom[0].Name)
			return nil, nil
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

// getNodesForPool returns a list of Nodes for MachineConfigPool 'pool'.
func (pc *ProfileCalculator) getNodesForPool(pool *mcfgv1.MachineConfigPool) ([]*corev1.Node, error) {
	selector, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid label selector %s in MachineConfigPool %s: %v", util.ObjectInfo(selector), pool.ObjectMeta.Name, err)
	}

	initialNodes, err := pc.listers.Nodes.List(selector)
	if err != nil {
		return nil, err
	}

	nodes := []*corev1.Node{}
	for _, n := range initialNodes {
		p, err := pc.getPrimaryPoolForNode(n)
		if err != nil {
			klog.Warningf("cannot get pool for node %q: %v", n.Name, err)
			continue
		}
		if p == nil {
			continue
		}
		if p.Name != pool.Name {
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// getPrimaryPoolForNode uses getPoolsForNode and returns the first one which is the one the node targets
func (pc *ProfileCalculator) getPrimaryPoolForNode(node *corev1.Node) (*mcfgv1.MachineConfigPool, error) {
	pools, err := pc.getPoolsForNode(node)
	if err != nil {
		return nil, err
	}
	if pools == nil {
		return nil, nil
	}
	return pools[0], nil
}

func machineConfigGenerationLogLine(bCmdline bool, bootcmdline string) string {
	var (
		sb strings.Builder
	)

	if bCmdline {
		sb.WriteString(" kernel parameters: [")
		sb.WriteString(bootcmdline)
		sb.WriteString("]")
	}

	return sb.String()
}
