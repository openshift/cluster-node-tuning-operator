package status

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
)

var _ status.Writer = &writer{}

type writer struct {
	controlPlaneClient client.Client
	dataPlaneClient    client.Client
	scheme             *runtime.Scheme
}

func NewWriter(controlPlaneClient client.Client, dataPlaneClient client.Client, scheme *runtime.Scheme) status.Writer {
	return &writer{controlPlaneClient: controlPlaneClient, dataPlaneClient: dataPlaneClient, scheme: scheme}
}

func (w *writer) Update(ctx context.Context, object client.Object, conditions []conditionsv1.Condition) error {
	performanceProfileCM, ok := object.(*corev1.ConfigMap)
	if !ok {
		return fmt.Errorf("wrong type conversion; want=ConfigMap got=%T", object)
	}

	profile, err := extractAndDecodeProfile(performanceProfileCM, object, w.scheme)
	if err != nil {
		return err
	}

	cm, err := ppStatusConfigMap(performanceProfileCM, profile.Name, w.scheme)
	if err != nil {
		return err
	}

	err = createOrUpdateStatusConfigMap(ctx, w.controlPlaneClient, cm, profile.Name, conditions)
	if err != nil {
		return err
	}

	return nil
}

func (w *writer) UpdateOwnedConditions(ctx context.Context, object client.Object) error {
	conditions, err := getTunedConditions(ctx, w.dataPlaneClient)
	if err != nil {
		return w.updateDegradedCondition(ctx, object, status.ConditionFailedGettingTunedProfileStatus, err)
	}

	// if conditions were not added then set as available
	if conditions == nil {
		conditions = status.GetAvailableConditions("")
	}
	return w.Update(ctx, object, conditions)
}

func (w *writer) updateDegradedCondition(ctx context.Context, instance client.Object, conditionState string, conditionError error) error {
	conditions := status.GetDegradedConditions(conditionState, conditionError.Error())
	if err := w.Update(ctx, instance, conditions); err != nil {
		return err
	}
	return conditionError
}
