package status

import (
	"context"
	"fmt"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"

	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
)

var _ Writer = &writer{}

type writer struct {
	client.Client
}

func NewWriter(c client.Client) Writer {
	return &writer{Client: c}
}

func (w *writer) Update(ctx context.Context, object client.Object, conditions []conditionsv1.Condition) error {
	profile, ok := object.(*performancev2.PerformanceProfile)
	if !ok {
		return fmt.Errorf("wrong type conversion; want=*PerformanceProfile got=%T", object)
	}
	return w.update(ctx, profile, conditions)
}

func (w *writer) UpdateOwnedConditions(ctx context.Context, object client.Object) error {
	profile, ok := object.(*performancev2.PerformanceProfile)
	if !ok {
		return fmt.Errorf("wrong type conversion; want=*PerformanceProfile got=%T", object)
	}

	// get kubelet false condition
	conditions, err := GetKubeletConditionsByProfile(ctx, w.Client, profile.Name)
	if err != nil {
		return w.updateDegradedCondition(profile, ConditionFailedGettingKubeletStatus, err)
	}

	// get MCP degraded conditions
	profileMCP, err := resources.GetMachineConfigPoolByProfile(ctx, w.Client, profile)
	if err != nil {
		return nil
	}

	if conditions == nil {
		conditions, err = GetMCPDegradedCondition(profileMCP)
		if err != nil {
			return w.updateDegradedCondition(profile, ConditionFailedGettingMCPStatus, err)
		}
	}

	// get tuned profile degraded conditions
	if conditions == nil {
		conditions, err = GetTunedConditionsByProfile(ctx, w.Client, profile)
		if err != nil {
			return w.updateDegradedCondition(profile, ConditionFailedGettingTunedProfileStatus, err)
		}
	}

	// if conditions were not added due to machine config pool status change then set as available
	if conditions == nil {
		conditions = GetAvailableConditions("")
	}
	return w.Update(ctx, profile, conditions)
}

func (w *writer) updateDegradedCondition(instance client.Object, conditionState string, conditionError error) error {
	conditions := GetDegradedConditions(conditionState, conditionError.Error())
	if err := w.Update(context.TODO(), instance, conditions); err != nil {
		klog.Errorf("failed to update performance profile %q status: %v", instance.GetName(), err)
		return err
	}
	return conditionError
}

func (w *writer) update(ctx context.Context, profile *performancev2.PerformanceProfile, conditions []conditionsv1.Condition) error {
	updatedStatus := CalculateUpdatedStatus(&profile.Status, profile.Name, conditions)
	if updatedStatus == nil {
		return nil
	}
	profile.Status = *updatedStatus

	klog.V(4).Infof("Updating the performance profile %q status", profile.Name)
	return w.Client.Status().Update(ctx, profile)
}
