package mcps

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	mcpUpdateTimeoutPerNode = 30
)

// GetByLabel returns all MCPs with the specified label
func GetByLabel(key, value string) ([]machineconfigv1.MachineConfigPool, error) {
	selector := labels.NewSelector()
	req, err := labels.NewRequirement(key, selection.Equals, []string{value})
	if err != nil {
		return nil, err
	}
	selector = selector.Add(*req)
	mcps := &machineconfigv1.MachineConfigPoolList{}
	if err := testclient.Client.List(context.TODO(), mcps, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}
	if len(mcps.Items) > 0 {
		return mcps.Items, nil
	}
	// fallback to look for a mcp with the same nodeselector.
	// key value may come from a node selector, so looking for a mcp
	// that targets the same nodes is legit
	if err := testclient.Client.List(context.TODO(), mcps); err != nil {
		return nil, err
	}
	res := []machineconfigv1.MachineConfigPool{}
	for _, item := range mcps.Items {
		if item.Spec.NodeSelector.MatchLabels[key] == value {
			res = append(res, item)
		}
		nodeRoleKey := components.NodeRoleLabelPrefix + value

		if _, ok := item.Spec.NodeSelector.MatchLabels[nodeRoleKey]; ok {
			res = append(res, item)
		}
	}
	return res, nil
}

// GetByName returns the MCP with the specified name
func GetByName(name string) (*machineconfigv1.MachineConfigPool, error) {
	mcp := &machineconfigv1.MachineConfigPool{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	err := testclient.GetWithRetry(context.TODO(), key, mcp)
	return mcp, err
}

// GetByNameNoRetry returns the MCP with the specified name without retrying to poke
// the api server
func GetByNameNoRetry(name string) (*machineconfigv1.MachineConfigPool, error) {
	mcp := &machineconfigv1.MachineConfigPool{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	err := testclient.Client.Get(context.TODO(), key, mcp)
	return mcp, err
}

// GetByProfile returns the MCP by a given performance profile
func GetByProfile(performanceProfile *performancev2.PerformanceProfile) (string, error) {
	mcpLabel := profile.GetMachineConfigLabel(performanceProfile)
	key, value := components.GetFirstKeyAndValue(mcpLabel)
	mcpsByLabel, err := GetByLabel(key, value)
	if err != nil {
		return "", err
	}
	performanceMCP := &mcpsByLabel[0]
	return performanceMCP.Name, nil
}

// New creates a new MCP with the given name and node selector
func New(mcpName string, nodeSelector map[string]string) *machineconfigv1.MachineConfigPool {
	return &machineconfigv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpName,
			Namespace: metav1.NamespaceNone,
			Labels:    map[string]string{components.MachineConfigRoleLabelKey: mcpName},
		},
		Spec: machineconfigv1.MachineConfigPoolSpec{
			MachineConfigSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      components.MachineConfigRoleLabelKey,
						Operator: "In",
						Values:   []string{"worker", mcpName},
					},
				},
			},
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: nodeSelector,
			},
		},
	}
}

// GetConditionStatus return the condition status of the given MCP and condition type
func GetConditionStatus(mcpName string, conditionType machineconfigv1.MachineConfigPoolConditionType) corev1.ConditionStatus {
	mcp, err := GetByNameNoRetry(mcpName)
	if err != nil {
		// In case of any error we just retry, as in case of single node cluster
		// the only node may be rebooting
		return corev1.ConditionUnknown
	}
	for _, condition := range mcp.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Status
		}
	}
	return corev1.ConditionUnknown
}

// GetConditionReason return the reason of the given MCP
func GetConditionReason(mcpName string, conditionType machineconfigv1.MachineConfigPoolConditionType) string {
	mcp, err := GetByName(mcpName)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "Failed getting MCP %q by name", mcpName)
	for _, condition := range mcp.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Reason
		}
	}
	return ""
}

// WaitForCondition waits for the MCP with given name having a condition of given type with given status
func WaitForCondition(mcpName string, conditionType machineconfigv1.MachineConfigPoolConditionType, conditionStatus corev1.ConditionStatus) {

	var cnfNodes []corev1.Node
	runningOnSingleNode, err := cluster.IsSingleNode()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	// checking in eventually as in case of single node cluster the only node may
	// be rebooting
	EventuallyWithOffset(1, func() error {
		mcp, err := GetByName(mcpName)
		if err != nil {
			return errors.Wrap(err, "Failed getting MCP by name")
		}

		nodeLabels := mcp.Spec.NodeSelector.MatchLabels
		key, _ := components.GetFirstKeyAndValue(nodeLabels)
		req, err := labels.NewRequirement(key, selection.Exists, []string{})
		if err != nil {
			return errors.Wrap(err, "Failed creating node selector")
		}

		selector := labels.NewSelector()
		selector = selector.Add(*req)
		cnfNodes, err = nodes.GetBySelector(selector)
		if err != nil {
			return errors.Wrap(err, "Failed getting nodes by selector")
		}

		testlog.Infof("MCP %q is targeting %v node(s)", mcp.Name, len(cnfNodes))
		return nil
	}, cluster.ComputeTestTimeout(10*time.Minute, runningOnSingleNode), 5*time.Second).ShouldNot(HaveOccurred(), "Failed to find CNF nodes by MCP %q", mcpName)

	// timeout should be based on the number of worker-cnf nodes
	timeout := time.Duration(len(cnfNodes)*mcpUpdateTimeoutPerNode) * time.Minute
	if len(cnfNodes) == 0 {
		timeout = 2 * time.Minute
	}

	EventuallyWithOffset(1, func() corev1.ConditionStatus {
		return GetConditionStatus(mcpName, conditionType)
	}, cluster.ComputeTestTimeout(timeout, runningOnSingleNode), 30*time.Second).Should(Equal(conditionStatus), "Failed to find condition status by MCP %q", mcpName)
}

// WaitForProfilePickedUp waits for the MCP with given name containing the MC created for the PerformanceProfile with the given name
func WaitForProfilePickedUp(mcpName string, profile *performancev2.PerformanceProfile) {
	runningOnSingleNode, err := cluster.IsSingleNode()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	testlog.Infof("Waiting for profile %s to be picked up by the %s machine config pool", profile.Name, mcpName)
	defer testlog.Infof("Profile %s picked up by the %s machine config pool", profile.Name, mcpName)
	EventuallyWithOffset(1, func() bool {
		mcp, err := GetByName(mcpName)
		// we ignore the error and just retry in case of single node cluster
		if err != nil {
			return false
		}
		for _, source := range mcp.Spec.Configuration.Source {
			if source.Name == machineconfig.GetMachineConfigName(profile) {
				return true
			}
		}
		return false
	}, cluster.ComputeTestTimeout(10*time.Minute, runningOnSingleNode), 30*time.Second).Should(BeTrue(), "PerformanceProfile's %q MC was not picked up by MCP %q in time", profile.Name, mcpName)
}

func Delete(name string) error {
	mcp := &machineconfigv1.MachineConfigPool{}
	if err := testclient.Client.Get(context.TODO(), types.NamespacedName{Name: name}, mcp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if err := testclient.Client.Delete(context.TODO(), mcp); err != nil {
		return err
	}

	return nil
}
