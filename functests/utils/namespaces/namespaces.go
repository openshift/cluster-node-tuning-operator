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

	testutils "github.com/openshift-kni/performance-addon-operators/functests/utils"
	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
)

// PerformanceOperator contains the name of the performance operator namespace
// default as recommended in
// https://docs.openshift.com/container-platform/4.6/scalability_and_performance/cnf-performance-addon-operator-for-low-latency-nodes.html#install-operator-cli_cnf-master
var PerformanceOperator string = "openshift-performance-addon-operator"

func init() {
	if operatorNS, ok := os.LookupEnv("PERFORMANCE_OPERATOR_NAMESPACE"); ok {
		PerformanceOperator = operatorNS
	}
}

// TestingNamespace is the namespace the tests will use for running test pods
var TestingNamespace = &corev1.Namespace{
	ObjectMeta: metav1.ObjectMeta{
		Name: testutils.NamespaceTesting,
	},
}

// WaitForDeletion waits until the namespace will be removed from the cluster
func WaitForDeletion(name string, timeout time.Duration) error {
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	return wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		ns := &corev1.Namespace{}
		if err := testclient.Client.Get(context.TODO(), key, ns); errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}
