package status

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	handler "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
)

const (
	PPstatusConfigMapPrefix = "status"
)

func createOrUpdateStatusConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap, profileName string, conditions []conditionsv1.Condition) error {
	err := cli.Get(ctx, client.ObjectKeyFromObject(cm), cm)

	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to read ConfigMap %q: %w", cm.Name, err)
	}

	prevStatus := &performancev2.PerformanceProfileStatus{}
	if err == nil {
		statusRaw, ok := cm.Data[operator.PPStatusConfigMapConfigKey]
		if !ok {
			return fmt.Errorf("status not found in performance profile status ConfigMap")
		}
		if err := StatusFromYAML([]byte(statusRaw), prevStatus); err != nil {
			return fmt.Errorf("failed to decode performance profile status from ConfigMap")
		}
	}
	updatedStatus := status.CalculateUpdatedStatus(ctx, prevStatus, profileName, conditions)
	if updatedStatus == nil {
		return nil
	}
	encodedStatus, encodeErr := StatusToYAML(updatedStatus)
	if encodeErr != nil {
		return encodeErr
	}
	cm.Data = map[string]string{
		operator.PPStatusConfigMapConfigKey: string(encodedStatus),
	}
	if k8serrors.IsNotFound(err) {
		if err := cli.Create(ctx, cm); err != nil {
			return fmt.Errorf("failed to create ConfigMap %q: %w", cm.Name, err)
		}
	} else {
		if err := cli.Update(ctx, cm); err != nil {
			return fmt.Errorf("failed to update ConfigMap %q: %w", cm.Name, err)
		}
	}

	return nil
}

func ppStatusConfigMap(performanceProfileCM *corev1.ConfigMap, profileName string, scheme *runtime.Scheme) (*corev1.ConfigMap, error) {
	nodePoolNamespacedName, ok := performanceProfileCM.Annotations[operator.HypershiftNodePoolLabel]
	if !ok {
		return nil, fmt.Errorf("annotation %q not found in ConfigMap %q annotations", operator.HypershiftNodePoolLabel, client.ObjectKeyFromObject(performanceProfileCM).String())
	}
	name := fmt.Sprintf("%s-%s", PPstatusConfigMapPrefix, performanceProfileCM.Name)
	cm := handler.ConfigMapMeta(name, profileName, performanceProfileCM.GetNamespace(), nodePoolNamespacedName)
	err := controllerutil.SetControllerReference(performanceProfileCM, cm, scheme)
	if err != nil {
		return nil, err
	}
	cm.Labels[operator.NtoGeneratedPerformanceProfileStatusLabel] = "true"

	return cm, nil
}

func extractAndDecodeProfile(performanceProfileCM *corev1.ConfigMap, object client.Object, scheme *runtime.Scheme) (*performancev2.PerformanceProfile, error) {
	s, ok := performanceProfileCM.Data[hypershift.TuningKey]
	if !ok {
		return nil, fmt.Errorf("key named %q not found in ConfigMap %q", hypershift.TuningKey, client.ObjectKeyFromObject(object).String())
	}

	profile := &performancev2.PerformanceProfile{}
	if err := hypershift.DecodeManifest([]byte(s), scheme, profile); err != nil {
		return nil, err
	}

	return profile, nil
}

func getTunedConditions(ctx context.Context, cli client.Client) ([]conditionsv1.Condition, error) {
	tunedProfileList := &tunedv1.ProfileList{}
	if err := cli.List(ctx, tunedProfileList); err != nil {
		klog.Errorf("Cannot list Tuned Profiles: %v", err)
		return nil, err
	}

	if len(tunedProfileList.Items) == 0 {
		return nil, fmt.Errorf("no tuned profiles has been found")
	}

	messageString := status.GetTunedProfilesMessage(tunedProfileList.Items)
	if len(messageString) == 0 {
		return nil, nil
	}

	return status.GetDegradedConditions(status.ConditionReasonTunedDegraded, messageString), nil
}

// EncodeToYAML encodes the PerformanceProfileStatus to YAML format.
func StatusToYAML(status *performancev2.PerformanceProfileStatus) ([]byte, error) {
	yamlData, err := yaml.Marshal(status)
	if err != nil {
		return nil, err
	}
	return yamlData, nil
}

// DecodeFromYAML decodes the YAML data into a PerformanceProfileStatus.
func StatusFromYAML(yamlData []byte, status *performancev2.PerformanceProfileStatus) error {
	err := yaml.Unmarshal(yamlData, status)
	if err != nil {
		return err
	}
	return nil
}
