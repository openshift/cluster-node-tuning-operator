package e2e

import (
	"context"
	"testing"
	"time"

	openshiftapi "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/apis"
	tuned "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1alpha1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	deploymentTimeout = 5 * time.Minute
	apiTimeout        = 10 * time.Second
)

// TestTunedStatus checks to see if the Tuned CR has our available status
func TestTunedStatus(t *testing.T) {
	ctx, client, ns := prepareTest(t)
	defer ctx.Cleanup()

	t.Log("=== Wait for TunedDeployment to be ready")
	if err := waitForTunedDeploymentReady(t, client, deploymentTimeout); err != nil {
		t.Errorf("failed to wait for TunedDeployment to get ready: %s", err)
	}
	t.Logf("tunedDeplyoment in %s is ready", ns)
}

func prepareTest(t *testing.T) (ctx *framework.TestCtx, client framework.FrameworkClient, namespace string) {
	tunedList := &tuned.TunedList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Tuned",
			APIVersion: tuned.SchemeGroupVersion.String(),
		},
	}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, tunedList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}

	ctx = framework.NewTestCtx(t)
	ns, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("failed to initialize namespace: %v", err)
	}
	return ctx, framework.Global.Client, ns
}

func waitForTunedDeploymentReady(t *testing.T, client framework.FrameworkClient, timeout time.Duration) error {
	cr := &tuned.Tuned{}
	err := wait.PollImmediate(time.Second, timeout, func() (done bool, err error) {
		ctx, cancel := testContext()
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: "default", Namespace: "openshift-cluster-node-tuning-operator"}, cr)
		if err != nil {
			return false, err
		}

		available := false

		for _, c := range cr.Status.Conditions {
			switch c.Type {
			case openshiftapi.OperatorStatusTypeAvailable:
				available = c.Status == openshiftapi.ConditionTrue
			}
		}
		if available {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Logf("failed to wait for TunedDeployment to get ready")
		t.Logf("last known version: %+v", cr)
	}

	return err
}

func testContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), apiTimeout)
}
