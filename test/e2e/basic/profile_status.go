package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
)

var _ = ginkgo.Describe("[basic][profile_status] Profile status conditions applied and not degraded", func() {
	const (
		pollInterval = 5 * time.Second
		waitDuration = 5 * time.Minute
		// The number of Profile status conditions.  Adjust when adding new conditions in the API.
		ProfileStatusConditions = 2
	)

	var explain string

	ginkgo.It("Profile status conditions applied and not degraded", func() {
		err := wait.PollUntilContextTimeout(context.TODO(), pollInterval, waitDuration, true, func(ctx context.Context) (bool, error) {
			profileList, err := cs.Profiles(ntoconfig.WatchNamespace()).List(ctx, metav1.ListOptions{})
			if err != nil {
				explain = err.Error()
				return false, nil
			}

			// Make sure that all Tuned Profiles have conditions of the type "Applied" == "True" and of the type "Degraded" == "False"
			for _, profile := range profileList.Items {
				if profile.Status.Conditions == nil || len(profile.Status.Conditions) != ProfileStatusConditions {
					explain = fmt.Sprintf("Profile %s expected to have %d status conditions set", profile.Name, ProfileStatusConditions)
					return false, nil
				}

				for _, condition := range profile.Status.Conditions {
					if condition.Type == tunedv1.TunedProfileApplied && condition.Status != corev1.ConditionTrue {
						explain = fmt.Sprintf("Profile %s not applied", profile.Name)
						return false, nil
					}
					if condition.Type == tunedv1.TunedDegraded && condition.Status != corev1.ConditionFalse {
						explain = fmt.Sprintf("Tuned degraded for %s", profile.Name)
						return false, nil
					}
					continue
				}
			}
			return true, nil
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), explain)
	})
})
