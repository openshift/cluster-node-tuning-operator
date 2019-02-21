// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/apis"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	deploymentTimeout = 5 * time.Minute
	apiTimeout        = 10 * time.Second
)

func TestOperatorAvailable(t *testing.T) {
	cfgv1client, err := ntoclient.GetCfgV1Client()
	if cfgv1client == nil {
		t.Errorf("failed to get a client: %v", err)
	}

	t.Log("=== Wait for tuned Cluster Operator to be available")
	if err := waitForTunedOperatorAvailable(t, cfgv1client, deploymentTimeout); err != nil {
		t.Errorf("failed to wait for tuned Cluster Operator to be available: %s", err)
	}
	t.Logf("tuned Cluster Operator is available")
}

func TestDefaultTunedExists(t *testing.T) {
	ctx, client, ns := prepareTest(t)
	defer ctx.Cleanup()

	t.Log("=== Wait for default Tuned CR to exist")
	if err := waitForTunedCR(t, client, deploymentTimeout); err != nil {
		t.Errorf("failed to wait for default Tuned CR to exist: %s", err)
	}
	t.Logf("tuned CR in %s/default exists", ns)
}

func prepareTest(t *testing.T) (ctx *framework.TestCtx, client framework.FrameworkClient, namespace string) {
	tunedList := &tunedv1.TunedList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Tuned",
			APIVersion: tunedv1.SchemeGroupVersion.String(),
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

func waitForTunedCR(t *testing.T, client framework.FrameworkClient, timeout time.Duration) error {
	cr := &tunedv1.Tuned{}
	err := wait.PollImmediate(time.Second, timeout, func() (done bool, err error) {
		ctx, cancel := testContext()
		defer cancel()
		err = client.Get(ctx, types.NamespacedName{Name: "default", Namespace: ntoconfig.OperatorNamespace()}, cr)
		if err != nil {
			return false, err
		}

		return true, nil
	})
	if err != nil {
		t.Logf("failed to wait for default Tuned CR to exist")
		t.Logf("last known version: %+v", cr)
	}

	return err
}

func waitForTunedOperatorAvailable(t *testing.T, cfgv1client *configv1client.ConfigV1Client, timeout time.Duration) error {
	clusterOperatorName := ntoconfig.OperatorName()

	co := &configv1.ClusterOperator{}

	err := wait.PollImmediate(time.Second, timeout, func() (done bool, err error) {
		co, err = cfgv1client.ClusterOperators().Get(clusterOperatorName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		for _, cond := range co.Status.Conditions {
			if cond.Type == configv1.OperatorAvailable &&
				cond.Status == configv1.ConditionTrue {
				return true, nil
			}
		}

		return false, nil
	})
	if err != nil {
		t.Logf("failed to wait for tuned Cluster Operator to be available")
		t.Logf("last known version: %+v", co)
	}

	return err
}

func testContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), apiTimeout)
}
