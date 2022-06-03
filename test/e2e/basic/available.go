package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

var _ = ginkgo.Describe("[basic][available] Node Tuning Operator availability", func() {
	const (
		pollInterval = 5 * time.Second
		waitDuration = 5 * time.Minute
	)

	var explain string

	ginkgo.It(fmt.Sprintf("ClusterOperator/%s available and not degraded", tunedv1.TunedClusterOperatorResourceName), func() {
		ginkgo.By(fmt.Sprintf("waiting for ClusterOperator/%s available", tunedv1.TunedClusterOperatorResourceName))
		err := util.WaitForClusterOperatorConditionStatus(cs, pollInterval, waitDuration, configv1.OperatorAvailable, configv1.ConditionTrue)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		ginkgo.By(fmt.Sprintf("waiting for ClusterOperator/%s not degraded", tunedv1.TunedClusterOperatorResourceName))
		err = util.WaitForClusterOperatorConditionStatus(cs, pollInterval, waitDuration, configv1.OperatorDegraded, configv1.ConditionFalse)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})

	ginkgo.It(fmt.Sprintf("Tuned/%s exists", tunedv1.TunedDefaultResourceName), func() {
		ginkgo.By(fmt.Sprintf("waiting for Tuned/%s existence", tunedv1.TunedDefaultResourceName))
		err := wait.PollImmediate(pollInterval, waitDuration, func() (bool, error) {
			_, err := cs.Tuneds(ntoconfig.OperatorNamespace()).Get(context.TODO(), tunedv1.TunedDefaultResourceName, metav1.GetOptions{})
			if err != nil {
				explain = err.Error()
				return false, nil
			}
			return true, nil
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)
	})
})
