package nodes

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift-kni/mixed-cpu-node-plugin/internal/pods"
)

const (
	// NamespaceMachineConfigOperator contains the namespace of the machine-config-opereator
	NamespaceMachineConfigOperator = "openshift-machine-config-operator"

	// ContainerMachineConfigDaemon contains the name of the machine-config-daemon container
	ContainerMachineConfigDaemon = "machine-config-daemon"
)

// GetMachineConfigDaemonByNode returns the machine-config-daemon pod that runs on the specified node
func GetMachineConfigDaemonByNode(ctx context.Context, kc *kubernetes.Clientset, nodeName string) (*corev1.Pod, error) {
	labelSelector := "k8s-app=machine-config-daemon"
	fieldSelector := fmt.Sprintf("spec.nodeName=%s", nodeName)
	mcds, err := kc.CoreV1().Pods(NamespaceMachineConfigOperator).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return nil, err
	}

	if len(mcds.Items) < 1 {
		return nil, fmt.Errorf("failed to find machine-config-daemon with label selector %q and field selector %q", labelSelector, fieldSelector)
	}
	return &mcds.Items[0], nil
}

// ExecCommand returns the output of the command execution on the machine-config-daemon pod that runs on the specified node
func ExecCommand(ctx context.Context, kc *kubernetes.Clientset, nodeName string, command []string) ([]byte, error) {
	mcd, err := GetMachineConfigDaemonByNode(ctx, kc, nodeName)
	if err != nil {
		return nil, err
	}

	return pods.Exec(kc, mcd, command)
}

func GetWorkers(ctx context.Context, c client.Client) ([]corev1.Node, error) {
	workerRoleLabel := "node-role.kubernetes.io/worker="
	selector, err := labels.Parse(workerRoleLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %q into selector", workerRoleLabel)
	}
	opts := &client.ListOptions{LabelSelector: selector}
	nodeList := &corev1.NodeList{}
	err = c.List(ctx, nodeList, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list worker nodes; %w", err)
	}
	return nodeList.Items, nil
}
