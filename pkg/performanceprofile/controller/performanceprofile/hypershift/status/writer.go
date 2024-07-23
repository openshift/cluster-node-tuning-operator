package status

import (
	"context"
	"fmt"
	"k8s.io/klog/v2"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
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
	instance, ok := object.(*corev1.ConfigMap)
	if !ok {
		return fmt.Errorf("wrong type conversion; want=*ConfigMap got=%T", object)
	}

	s, ok := instance.Data[hypershiftconsts.TuningKey]
	if !ok {
		return fmt.Errorf("key named %q not found in ConfigMap %q", hypershiftconsts.TuningKey, client.ObjectKeyFromObject(instance).String())
	}

	profile := &performancev2.PerformanceProfile{}
	if _, err := hypershift.DecodeManifest([]byte(s), w.scheme, profile); err != nil {
		return err
	}
	klog.V(4).InfoS("PerformanceProfile decoded successfully from ConfigMap data", "PerformanceProfileName", profile.Name, "ConfigMapName", instance.GetName())

	cm, err := makePerformanceProfileStatusConfigMap(instance, profile.Name, w.scheme)
	if err != nil {
		return err
	}
	return createOrUpdateStatusConfigMap(ctx, w.controlPlaneClient, cm, profile.Name, conditions)
}

func (w *writer) UpdateOwnedConditions(ctx context.Context, object client.Object) error {
	conditions, err := getTunedConditions(ctx, w.dataPlaneClient)
	if err != nil {
		return w.updateDegradedCondition(ctx, object, status.ConditionFailedGettingTunedProfileStatus, err)
	}

	// if conditions were not added, then set as available
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
