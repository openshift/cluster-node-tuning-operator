package __latency_testing_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/images"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/junit"
	testlog "github.com/openshift-kni/performance-addon-operators/functests/utils/log"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/namespaces"
	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

var prePullNamespace = &corev1.Namespace{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testing-prepull",
	},
}

var _ = AfterSuite(func() {
	prePullNamespaceName := prePullNamespace.Name
	err := testclient.Client.Delete(context.TODO(), prePullNamespace)
	testlog.Infof("deleted namespace %q err=%v", prePullNamespace.Name, err)
	Expect(err).ToNot(HaveOccurred())
	err = namespaces.WaitForDeletion(prePullNamespaceName, 5*time.Minute)
})

func Test5LatencyTesting(t *testing.T) {
	RegisterFailHandler(Fail)

	if !testclient.ClientsEnabled {
		t.Fatalf("client not enabled")
	}

	if err := createNamespace(); err != nil {
		t.Fatalf("cannot create the namespace: %v", err)
	}

	ds, err := images.PrePull(testclient.Client, images.Test(), prePullNamespace.Name, "cnf-tests")
	if err != nil {
		data, _ := json.Marshal(ds) // we can safely skip errors
		testlog.Infof("DaemonSet %s/%s image=%q status:\n%s", ds.Namespace, ds.Name, images.Test(), string(data))
		t.Fatalf("cannot prepull image %q: %v", images.Test(), err)
	}

	rr := []Reporter{}
	if ginkgo_reporters.Polarion.Run {
		rr = append(rr, &ginkgo_reporters.Polarion)
	}
	rr = append(rr, junit.NewJUnitReporter("latency_testing"))
	RunSpecsWithDefaultAndCustomReporters(t, "Performance Addon Operator latency tools testing", rr)
}

func createNamespace() error {
	err := testclient.Client.Create(context.TODO(), prePullNamespace)
	if errors.IsAlreadyExists(err) {
		testlog.Warningf("%q namespace already exists, that is unexpected", prePullNamespace.Name)
		return nil
	}
	testlog.Infof("created namespace %q err=%v", prePullNamespace.Name, err)
	return err
}
