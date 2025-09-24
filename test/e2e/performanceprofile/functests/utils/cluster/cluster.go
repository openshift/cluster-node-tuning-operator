package cluster

import (
	"context"
	"time"

	clientconfigv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
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
	// Get the rest.Config using the helper function
	cfg, err := config.GetConfig()
	if err != nil {
		return false, err
	}

	openshiftConfigClient := clientconfigv1.NewForConfigOrDie(cfg)
	schedulerInfo, err := openshiftConfigClient.Schedulers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	return schedulerInfo.Spec.MastersSchedulable, nil
}
