package status

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	handler "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/components"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
)

func createOrUpdateStatusConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap, profileName string, conditions []conditionsv1.Condition) error {
	prevStatus, prevStatusFound, err := getPreviousStatusFrom(ctx, cli, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cm.Name,
			Namespace: cm.Namespace,
		},
	})
	if err != nil {
		return err
	}
	npName := cm.Labels[hypershiftconsts.NodePoolNameLabel]
	updatedStatus := status.CalculateUpdated(prevStatus, profileName, npName, conditions)
	if updatedStatus == nil {
		return nil
	}
	encodedStatus, encodeErr := yaml.Marshal(updatedStatus)
	if encodeErr != nil {
		return encodeErr
	}
	cm.Data = map[string]string{
		hypershiftconsts.PerformanceProfileStatusKey: string(encodedStatus),
	}

	key := client.ObjectKeyFromObject(cm)
	if prevStatusFound {
		klog.InfoS("Updating status", "ConfigMap", key.String())
		if err := cli.Update(ctx, cm); err != nil {
			return fmt.Errorf("failed to update status ConfigMap %q: %w", key.String(), err)
		}
		klog.InfoS("Status updated ", "ConfigMap", key.String())
		return nil
	}
	klog.InfoS("Creating status", "ConfigMap", key.String())
	if err := cli.Create(ctx, cm); err != nil {
		return fmt.Errorf("failed to create ConfigMap %q: %w", key.String(), err)
	}
	klog.InfoS("Status created", "ConfigMap", key.String())
	return nil
}

func makePerformanceProfileStatusConfigMap(instance *corev1.ConfigMap, profileName string, scheme *runtime.Scheme) (*corev1.ConfigMap, error) {
	nodePoolNamespacedName, ok := instance.Annotations[hypershiftconsts.NodePoolNameLabel]
	if !ok {
		return nil, fmt.Errorf("annotation %q not found in ConfigMap %q annotations", hypershiftconsts.NodePoolNameLabel, client.ObjectKeyFromObject(instance).String())
	}
	cm := handler.ConfigMapMeta(hypershift.GetStatusConfigMapName(instance.Name), profileName, instance.GetNamespace(), nodePoolNamespacedName)
	err := controllerutil.SetControllerReference(instance, cm, scheme)
	if err != nil {
		return nil, err
	}
	cm.Labels[hypershiftconsts.NTOGeneratedPerformanceProfileStatusConfigMapLabel] = "true"

	return cm, nil
}

func getTunedConditions(ctx context.Context, cli client.Client) ([]conditionsv1.Condition, error) {
	tunedProfileList := &tunedv1.ProfileList{}
	if err := cli.List(ctx, tunedProfileList); err != nil {
		klog.Errorf("Cannot list Tuned Profiles: %v", err)
		return nil, err
	}

	messageString := status.GetTunedProfilesMessage(tunedProfileList.Items)
	if len(messageString) == 0 {
		return nil, nil
	}
	return status.GetDegradedConditions(status.ConditionReasonTunedDegraded, messageString), nil
}

func getPreviousStatusFrom(ctx context.Context, cli client.Client, cm *corev1.ConfigMap) (*performancev2.PerformanceProfileStatus, bool, error) {
	prevStatus := &performancev2.PerformanceProfileStatus{}
	key := client.ObjectKeyFromObject(cm)
	err := cli.Get(ctx, key, cm)

	if err != nil && !k8serrors.IsNotFound(err) {
		return prevStatus, false, fmt.Errorf("failed to Get ConfigMap %q: %w", key.String(), err)
	}

	if k8serrors.IsNotFound(err) {
		return prevStatus, false, nil
	}

	statusRaw, ok := cm.Data[hypershiftconsts.PerformanceProfileStatusKey]
	if !ok {
		return prevStatus, false, fmt.Errorf("status not found in PerformanceProfile status ConfigMap %q", key.String())
	}

	if err := yaml.Unmarshal([]byte(statusRaw), prevStatus); err != nil {
		return prevStatus, false, fmt.Errorf("failed to decode PerformanceProfile status from ConfigMap %q", key.String())
	}
	return prevStatus, true, nil
}
