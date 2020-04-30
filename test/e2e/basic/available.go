package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
)

var _ = Describe("Node Tuning Operator: availability", func() {
	It(fmt.Sprintf("clusteroperator/%s available", tunedv1.TunedClusterOperatorResourceName), func() {
		By(fmt.Sprintf("wait for clusteroperator/%s available", tunedv1.TunedClusterOperatorResourceName))
		err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
			co, err := cs.ClusterOperators().Get(context.TODO(), tunedv1.TunedClusterOperatorResourceName, metav1.GetOptions{})
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
		Expect(err).NotTo(HaveOccurred())
	})

	It(fmt.Sprintf("tuned/%s exists", tunedv1.TunedDefaultResourceName), func() {
		By(fmt.Sprintf("wait for tuned/%s existence", tunedv1.TunedDefaultResourceName))
		err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
			_, err := cs.Tuneds(ntoconfig.OperatorNamespace()).Get(context.TODO(), tunedv1.TunedDefaultResourceName, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			return true, nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
