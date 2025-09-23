package cluster

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
)

// IsSingleNode validates if the environment is single node cluster
func IsSingleNode() (bool, error) {
	nodes := &corev1.NodeList{}
	if err := testclient.DataPlaneClient.List(context.TODO(), nodes, &client.ListOptions{}); err != nil {
		return false, err
	}
	return len(nodes.Items) == 1, nil
}

// ComputeTestTimeout returns the desired timeout for a test based on a given base timeout.
// If the tested cluster is Single-Node it needs more time to react (due to being highly loaded) so we double the given timeout.
func ComputeTestTimeout(baseTimeout time.Duration, isSno bool) time.Duration {
	testTimeout := baseTimeout
	if isSno {
		testTimeout += baseTimeout
	}

	return testTimeout
}

// Check if the control plane nodes are schedulable
func IsControlPlaneSchedulable() (bool, error) {
	controlPlaneNodesLabel := make(map[string]string)
	controlPlaneNodesLabel["node-role.kubernetes.io/control-plane"] = ""
	selector := labels.SelectorFromSet(controlPlaneNodesLabel)
	controlPlaneNodes := &corev1.NodeList{}
	if err := testclient.DataPlaneClient.List(context.TODO(), controlPlaneNodes, &client.ListOptions{LabelSelector: selector}); err != nil {
		return false, err
	}

	if len(controlPlaneNodes.Items) == 0 {
		return false, fmt.Errorf("unable to fetch control plane nodes")
	}
	for _, v := range controlPlaneNodes.Items {
		// when control plane are schedulable, All taints are removed
		if len(v.Spec.Taints) != 0 {
			return false, nil // Not schedulable, but not an error
		}
	}
	return true, nil
}
