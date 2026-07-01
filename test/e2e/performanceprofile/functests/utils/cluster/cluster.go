package cluster

import (
	"context"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// Check if the control plane nodes are schedulable, returns true if schedulable else false
func IsControlPlaneSchedulable(ctx context.Context) (bool, error) {
	scheduler := &configv1.Scheduler{}
	if err := testclient.ControlPlaneClient.Get(ctx, client.ObjectKey{Name: "cluster"}, scheduler); err != nil {
		return false, err
	}
	return scheduler.Spec.MastersSchedulable, nil
}

// IsWorkloadPartitioningEnabled checks whether CPU partitioning is enabled
// cluster-wide by querying the Infrastructure resource's CPUPartitioning status.
// The caller must pass the appropriate client: on HyperShift the controller
// reads the hosted cluster's Infrastructure, so tests should use the data-plane
// client (testclient.Client) to match.
func IsWorkloadPartitioningEnabled(ctx context.Context, cli client.Client) (bool, error) {
	infra := &configv1.Infrastructure{}
	if err := cli.Get(ctx, client.ObjectKey{Name: "cluster"}, infra); err != nil {
		return false, err
	}
	return infra.Status.CPUPartitioning == configv1.CPUPartitioningAllNodes, nil
}
