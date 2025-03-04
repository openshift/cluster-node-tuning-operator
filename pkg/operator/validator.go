package operator

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
)

type DuplicateProfileError struct {
	crName map[string]string
	//                ^^^^^^ TuneD profile duplicate name (record only one duplicate to keep things simple).
	//         ^^^^^^ Tuned CR name containing a duplicate TuneD profile.
}

func (e *DuplicateProfileError) Error() string {
	return fmt.Sprintf("ERROR: duplicate TuneD profiles found: %v", e.crName)
}

// validateTunedCRs checks all Tuned CR for potential misconfiguration,
// obsolete functionality and updates their status as necessary.
// Returns error only if either List()/Update() of/on k8s objects needs
// to be requeued.
func (c *Controller) validateTunedCRs() error {
	allTunedValid := true

	tunedList, err := c.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list Tuned: %v", err)
	}

	_, errProfilesGet := tunedProfilesGet(tunedList)

	for _, tuned := range tunedList {
		tunedValid := true
		message := "" // Do not add any unnecessary clutter when configuration is valid.

		switch e := errProfilesGet.(type) {
		case nil:
		case *DuplicateProfileError:
			// We have TuneD profiles with the same name and different contents.
			if val, found := e.crName[tuned.Name]; found {
				tunedValid = false
				allTunedValid = false
				klog.Errorf("duplicate TuneD profile %s with conflicting content detected in tuned/%s.", val, tuned.Name)
				message = fmt.Sprintf("Duplicate TuneD profile %q with conflicting content detected.", val)
			}
		}

		tuned = tuned.DeepCopy() // Make sure we do not modify objects in cache

		if len(tuned.Status.Conditions) == 0 {
			tuned.Status.Conditions = initializeTunedStatusConditions()
		}
		tuned.Status.Conditions = computeStatusConditions(tunedValid, message, tuned.Status.Conditions)

		_, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).UpdateStatus(context.TODO(), tuned, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update Tuned %s status: %v", tuned.Name, err)
		}
	}
	// Populate NTO metrics to create an alert if the misconfiguration persists.
	metrics.InvalidTunedExist(!allTunedValid)

	return nil
}
