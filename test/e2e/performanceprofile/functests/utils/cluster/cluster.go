package cluster

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
)

// IsSingleNode validates if the environment is single node cluster
func IsSingleNode() (bool, error) {
	nodes := &corev1.NodeList{}
	if err := testclient.Client.List(context.TODO(), nodes, &client.ListOptions{}); err != nil {
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
