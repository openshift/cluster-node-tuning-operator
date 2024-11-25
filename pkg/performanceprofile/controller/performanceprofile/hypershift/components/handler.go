package components

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"
)

var _ components.Handler = &handler{}

type handler struct {
	controlPlaneClient client.Client
	dataPlaneClient    client.Client
	scheme             *runtime.Scheme
}

func NewHandler(controlPlaneClient, dataPlaneClient client.Client, scheme *runtime.Scheme) components.Handler {
	return &handler{
		controlPlaneClient: controlPlaneClient,
		dataPlaneClient:    dataPlaneClient,
		scheme:             scheme,
	}
}

func (h *handler) Delete(ctx context.Context, profileName string) error {
	// ConfigMap is marked for deletion and waiting for finalizers to be empty
	// so better to clean up and delete the objects.

	// delete RtClass, as right now all the other components created from this PP
	// are embedded into ConfigMaps, which has an OwnerReference with the PP configmap
	// and will be deleted by k8s machinery triggering the deletion procedure of the embedded elements.
	rtName := components.GetComponentName(profileName, components.ComponentNamePrefix)
	rtClass, err := resources.GetRuntimeClass(ctx, h.dataPlaneClient, rtName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// rtClass not found so nothing to delete, so delete process has finished
			return nil
		}
		return fmt.Errorf("unable to read RuntimeClass %q, error: %w. Unable to finalize deletion procedure properly", rtName, err)
	}

	err = h.dataPlaneClient.Delete(ctx, rtClass)
	return err
}

func (h *handler) Exists(ctx context.Context, profileName string) bool {
	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	if _, err := resources.GetRuntimeClass(ctx, h.dataPlaneClient, name); !k8serrors.IsNotFound(err) {
		klog.Infof("Runtime class %q exists in the hosted cluster", name)
		return true
	}
	return false
}

func (h *handler) Apply(ctx context.Context, obj client.Object, recorder record.EventRecorder, options *components.Options) error {
	instance, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return fmt.Errorf("wrong type conversion; want=*ConfigMap got=%T", obj)
	}

	s, ok := instance.Data[hypershiftconsts.TuningKey]
	if !ok {
		return fmt.Errorf("key named %q not found in ConfigMap %q", hypershiftconsts.TuningKey, client.ObjectKeyFromObject(obj).String())
	}

	profile := &performancev2.PerformanceProfile{}
	if _, err := hypershift.DecodeManifest([]byte(s), h.scheme, profile); err != nil {
		return err
	}
	klog.V(4).InfoS("PerformanceProfile decoded successfully from ConfigMap data", "PerformanceProfileName", profile.Name, "ConfigMapName", instance.GetName())

	if profileutil.IsPaused(profile) {
		klog.Infof("ignoring reconcile loop for pause performance profile %s", profile.Name)
		return nil
	}
	// set missing options
	options.MachineConfig.MixedCPUsEnabled = options.MixedCPUsFeatureGateEnabled && profileutil.IsMixedCPUsEnabled(profile)

	mfs, err := manifestset.GetNewComponents(profile, options)
	if err != nil {
		return err
	}

	// get mutated machine config
	mcMutated, err := resources.GetMutatedMachineConfig(ctx, h.controlPlaneClient, mfs.MachineConfig)
	if err != nil {
		return err
	}

	// get mutated kubelet config
	kcMutated, err := resources.GetMutatedKubeletConfig(ctx, h.controlPlaneClient, mfs.KubeletConfig)
	if err != nil {
		return err
	}

	// get mutated performance tuned
	performanceTunedMutated, err := resources.GetMutatedTuned(ctx, h.controlPlaneClient, mfs.Tuned)
	if err != nil {
		return err
	}

	// get mutated RuntimeClass
	runtimeClassMutated, err := resources.GetMutatedRuntimeClass(ctx, h.dataPlaneClient, mfs.RuntimeClass)
	if err != nil {
		return err
	}

	updated := mcMutated != nil ||
		kcMutated != nil ||
		performanceTunedMutated != nil ||
		runtimeClassMutated != nil

	// do not update any resources if no changes are present and continue to the status update
	if !updated {
		return nil
	}

	if mcMutated != nil {
		cm, err := EncapsulateObjInConfigMap(h.scheme, instance, mfs.MachineConfig, profile.Name, hypershiftconsts.ConfigKey, map[string]string{hypershiftconsts.NTOGeneratedMachineConfigLabel: "true"})
		if err != nil {
			return err
		}
		err = createOrUpdateMachineConfigConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if kcMutated != nil {
		cm, err := EncapsulateObjInConfigMap(h.scheme, instance, mfs.KubeletConfig, profile.Name, hypershiftconsts.ConfigKey, map[string]string{
			hypershiftconsts.NTOGeneratedMachineConfigLabel: "true",
			hypershiftconsts.KubeletConfigConfigMapLabel:    "true",
		})
		if err != nil {
			return err
		}
		err = createOrUpdateKubeletConfigConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if performanceTunedMutated != nil {
		cm, err := EncapsulateObjInConfigMap(h.scheme, instance, mfs.Tuned, profile.Name, hypershiftconsts.TuningKey, map[string]string{hypershiftconsts.ControllerGeneratedTunedConfigMapLabel: "true"})
		if err != nil {
			return err
		}
		err = createOrUpdateTunedConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if runtimeClassMutated != nil {
		err = resources.CreateOrUpdateRuntimeClass(ctx, h.dataPlaneClient, runtimeClassMutated)
		if err != nil {
			return err
		}
	}

	klog.InfoS("Processed ok", "instanceName", instance.Name)
	recorder.Eventf(instance, corev1.EventTypeNormal, "Creation succeeded", "Succeeded to create all components for PerformanceProfile")
	return nil
}

func EncapsulateObjInConfigMap(scheme *runtime.Scheme, instance *corev1.ConfigMap, object client.Object, profileName, dataKey string, objectLabels map[string]string) (*corev1.ConfigMap, error) {
	encodedObj, err := hypershift.EncodeManifest(object, scheme)
	if err != nil {
		return nil, err
	}
	nodePoolNamespacedName, ok := instance.Annotations[hypershiftconsts.NodePoolNameLabel]
	if !ok {
		return nil, fmt.Errorf("annotation %q not found in ConfigMap %q annotations", hypershiftconsts.NodePoolNameLabel, client.ObjectKeyFromObject(instance).String())
	}

	name := fmt.Sprintf("%s-%s", strings.ToLower(object.GetObjectKind().GroupVersionKind().Kind), instance.Name)
	cm := ConfigMapMeta(name, profileName, instance.GetNamespace(), nodePoolNamespacedName)
	err = controllerutil.SetControllerReference(instance, cm, scheme)
	if err != nil {
		return nil, err
	}
	for v, k := range objectLabels {
		cm.Labels[v] = k
	}
	cm.Data = map[string]string{
		dataKey: string(encodedObj),
	}
	return cm, nil
}

func createOrUpdateTunedConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap) error {
	updateFunc := func(orig, dst *corev1.ConfigMap) {
		dst.Data[hypershiftconsts.TuningKey] = orig.Data[hypershiftconsts.TuningKey]
	}
	return createOrUpdateConfigMap(ctx, cli, cm, updateFunc)
}

func createOrUpdateMachineConfigConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap) error {
	machineconfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) {
		dst.Data[hypershiftconsts.ConfigKey] = orig.Data[hypershiftconsts.ConfigKey]
	}
	return createOrUpdateConfigMap(ctx, cli, cm, machineconfigConfigMapUpdateFunc)
}

func createOrUpdateKubeletConfigConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap) error {
	kubeletConfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) {
		dst.Data[hypershiftconsts.ConfigKey] = orig.Data[hypershiftconsts.ConfigKey]
	}
	return createOrUpdateConfigMap(ctx, cli, cm, kubeletConfigConfigMapUpdateFunc)
}

// ConfigMapMeta return a ConfigMap that can be used to encapsulate
// cluster scoped objects within the desired Namespace
func ConfigMapMeta(name, profileName, namespace, npNamespacedName string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				hypershiftconsts.PerformanceProfileNameLabel: profileName,
				hypershiftconsts.NodePoolNameLabel:           parseNamespacedName(npNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftconsts.NodePoolNameLabel: npNamespacedName,
			},
		},
	}
}

// parseNamespacedName expects a string with the format "namespace/name"
// and returns the name only.
// If given a string in the format "name" returns "name".
func parseNamespacedName(namespacedName string) string {
	parts := strings.SplitN(namespacedName, "/", 2)
	if len(parts) > 1 {
		return parts[1]
	}
	return parts[0]
}

func createOrUpdateConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap, updateFunc func(origin, destination *corev1.ConfigMap)) error {
	tcm := &corev1.ConfigMap{}
	err := cli.Get(ctx, client.ObjectKeyFromObject(cm), tcm)
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to read configmap %q: %w", cm.Name, err)
	} else if k8serrors.IsNotFound(err) {
		//create
		if err := cli.Create(ctx, cm); err != nil {
			return fmt.Errorf("failed to create configmap %q: %w", cm.Name, err)
		}
	} else {
		// update
		updateFunc(cm, tcm)
		if err := cli.Update(ctx, tcm); err != nil {
			return fmt.Errorf("failed to update configmap %q: %w", cm.Name, err)
		}
	}
	return nil
}
