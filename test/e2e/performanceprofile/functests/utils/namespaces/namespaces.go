package namespaces

import (
	"context"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
)

// PerformanceOperator contains the name of the performance operator namespace
// default as recommended in
// https://docs.openshift.com/container-platform/4.6/scalability_and_performance/cnf-performance-addon-operator-for-low-latency-nodes.html#install-operator-cli_cnf-master
var PerformanceOperator string = "openshift-cluster-node-tuning-operator"

func init() {
	if operatorNS, ok := os.LookupEnv("PERFORMANCE_OPERATOR_NAMESPACE"); ok {
		PerformanceOperator = operatorNS
	}
}

// TestingNamespace is the namespace the tests will use for running test pods
var TestingNamespace = &corev1.Namespace{
	ObjectMeta: metav1.ObjectMeta{
		Name: testutils.NamespaceTesting,
		Annotations: map[string]string{
			"workload.mixedcpus.openshift.io/allowed": "",
		},
		Labels: map[string]string{
			"security.openshift.io/scc.podSecurityLabelSync": "false",
			"pod-security.kubernetes.io/audit":               "privileged",
			"pod-security.kubernetes.io/enforce":             "privileged",
			"pod-security.kubernetes.io/warn":                "privileged",
		},
	},
}

// WaitForDeletion waits until the namespace will be removed from the cluster
func WaitForDeletion(name string, timeout time.Duration) error {
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	return wait.PollUntilContextTimeout(context.TODO(), time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		ns := &corev1.Namespace{}
		if err := testclient.Client.Get(ctx, key, ns); errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}
