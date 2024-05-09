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

	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"
)

const (
	hypershiftPerformanceProfileNameLabel = "hypershift.openshift.io/performanceProfileName"
	hypershiftNodePoolLabel               = "hypershift.openshift.io/nodePool"
	tunedConfigMapLabel                   = "hypershift.openshift.io/tuned-config"
	tunedConfigMapConfigKey               = "tuning"
	mcoConfigMapConfigKey                 = "config"
	ntoGeneratedMachineConfigLabel        = "hypershift.openshift.io/nto-generated-machine-config"
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
	operatorNamespace := config.OperatorNamespace()
	tunedName := components.GetComponentName(profileName, components.ProfileNamePerformance)
	if _, err := resources.GetTuned(ctx, h.controlPlaneClient, tunedName, operatorNamespace); !k8serrors.IsNotFound(err) {
		klog.Infof("Tuned %q is still exists under the namespace %q", tunedName, operatorNamespace)
		return true
	}

	name := components.GetComponentName(profileName, components.ComponentNamePrefix)
	if _, err := resources.GetKubeletConfig(ctx, h.controlPlaneClient, name); !k8serrors.IsNotFound(err) {
		klog.Infof("Kubelet Config %q exists under the namespace %q", name, operatorNamespace)
		return true
	}

	if _, err := resources.GetRuntimeClass(ctx, h.dataPlaneClient, name); !k8serrors.IsNotFound(err) {
		klog.Infof("Runtime class %q exists under the hosted cluster", name)
		return true
	}

	if _, err := resources.GetMachineConfig(ctx, h.controlPlaneClient, machineconfig.GetMachineConfigName(profileName)); !k8serrors.IsNotFound(err) {
		klog.Infof("Machine Config %q exists under the namespace %q", name, operatorNamespace)
		return true
	}
	return false
}

func (h *handler) Apply(ctx context.Context, obj client.Object, recorder record.EventRecorder, options *components.Options) error {
	// TODO  handle naming
	// Jose left the following comment:
	// Make PerformanceProfile names unique if a PerformanceProfile is duplicated across NodePools
	// for example, if one ConfigMap is referenced in multiple NodePools
	// need to check whether this comment is relevant or not.
	instance, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return fmt.Errorf("wrong type conversion; want=ConfigMap got=%T", obj)
	}

	s, ok := instance.Data[hypershift.TuningKey]
	if !ok {
		return fmt.Errorf("key named %q not found in ConfigMap %q", hypershift.TuningKey, client.ObjectKeyFromObject(obj).String())
	}

	profile := &performancev2.PerformanceProfile{}
	if err := hypershift.DecodeManifest([]byte(s), h.scheme, profile); err != nil {
		return err
	}

	if profileutil.IsPaused(profile) {
		klog.Infof("ignoring reconcile loop for pause performance profile %s", profile.Name)
		return nil
	}

	ctrRuntime, err := h.getContainerRuntimeName(ctx, profile)
	if err != nil {
		return fmt.Errorf("could not determine high-performance runtime class container-runtime for profile %q; %w", profile.Name, err)
	}
	klog.Infof("using %q as high-performance runtime class container-runtime for profile %q", ctrRuntime, profile.Name)
	options.MachineConfig.DefaultRuntime = ctrRuntime

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
		cm, err := h.encapsulateObjInConfigMap(instance, mfs.MachineConfig, profile.Name, mcoConfigMapConfigKey, ntoGeneratedMachineConfigLabel)
		if err != nil {
			return err
		}
		err = createOrUpdateMachineConfigConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if kcMutated != nil {
		cm, err := h.encapsulateObjInConfigMap(instance, mfs.KubeletConfig, profile.Name, mcoConfigMapConfigKey, ntoGeneratedMachineConfigLabel)
		if err != nil {
			return err
		}
		err = createOrUpdateKubeletConfigConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if performanceTunedMutated != nil {
		cm, err := h.encapsulateObjInConfigMap(instance, mfs.Tuned, profile.Name, tunedConfigMapConfigKey, tunedConfigMapLabel)
		if err != nil {
			return err
		}
		err = createOrUpdateTunedConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if runtimeClassMutated != nil {
		err = resources.CreateOrUpdateRuntimeClass(ctx, h.dataPlaneClient, mfs.RuntimeClass)
		if err != nil {
			return err
		}
	}

	klog.InfoS("Processed ok", "instanceName", instance.Name)
	recorder.Eventf(instance, corev1.EventTypeNormal, "Creation succeeded", "Succeeded to create all components for PerformanceProfile")
	return nil
}

func (h *handler) getContainerRuntimeName(ctx context.Context, profile *performancev2.PerformanceProfile) (mcov1.ContainerRuntimeDefaultRuntime, error) {
	//TODO we need to figure out how containerruntimeconfig works on hypershift
	return mcov1.ContainerRuntimeDefaultRuntimeRunc, nil
}

func (h *handler) encapsulateObjInConfigMap(instance *corev1.ConfigMap, object client.Object, profileName, dataKey, objectLabel string) (*corev1.ConfigMap, error) {
	encodedObj, err := hypershift.EncodeManifest(object, h.scheme)
	if err != nil {
		return nil, err
	}
	nodePoolNamespacedName, ok := instance.Annotations[hypershiftNodePoolLabel]
	if !ok {
		return nil, fmt.Errorf("annotation %q not found in ConfigMap %q annotations", hypershiftNodePoolLabel, client.ObjectKeyFromObject(instance).String())
	}

	name := fmt.Sprintf("%s-%s", strings.ToLower(object.GetObjectKind().GroupVersionKind().Kind), instance.Name)
	cm := configMapMeta(name, profileName, instance.GetNamespace(), nodePoolNamespacedName)
	err = controllerutil.SetControllerReference(instance, cm, h.scheme)
	if err != nil {
		return nil, err
	}
	cm.Labels[objectLabel] = "true"
	cm.Data = map[string]string{
		dataKey: string(encodedObj),
	}
	return cm, nil
}

func createOrUpdateTunedConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap) error {
	updateFunc := func(orig, dst *corev1.ConfigMap) {
		dst.Data[tunedConfigMapConfigKey] = orig.Data[tunedConfigMapConfigKey]
	}
	return createOrUpdateConfigMap(ctx, cli, cm, updateFunc)
}

func createOrUpdateMachineConfigConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap) error {
	machineconfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) {
		dst.Data[mcoConfigMapConfigKey] = orig.Data[mcoConfigMapConfigKey]
	}
	return createOrUpdateConfigMap(ctx, cli, cm, machineconfigConfigMapUpdateFunc)
}

func createOrUpdateKubeletConfigConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap) error {
	kubeletConfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) {
		dst.Data[mcoConfigMapConfigKey] = orig.Data[mcoConfigMapConfigKey]
	}
	return createOrUpdateConfigMap(ctx, cli, cm, kubeletConfigConfigMapUpdateFunc)
}

// configMapMeta return a ConfigMap that can be used to encapsulate
// cluster scoped objects within the desired Namespace
func configMapMeta(name, profileName, namespace, npNamespacedName string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				hypershiftPerformanceProfileNameLabel: profileName,
				hypershiftNodePoolLabel:               parseNamespacedName(npNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftNodePoolLabel: npNamespacedName,
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
