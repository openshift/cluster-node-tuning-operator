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
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/resources"
)

const (
	hypershiftNodePoolLabel        = "hypershift.openshift.io/nodePool"
	tunedConfigMapLabel            = "hypershift.openshift.io/tuned-config"
	ntoGeneratedMachineConfigLabel = "hypershift.openshift.io/nto-generated-machine-config"
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
		cm, err := EncapsulateObjInConfigMap(h.scheme, instance, mfs.MachineConfig, profile.Name, hypershiftconsts.ConfigKey, ntoGeneratedMachineConfigLabel)
		if err != nil {
			return err
		}
		err = createOrUpdateMachineConfigConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if kcMutated != nil {
		cm, err := EncapsulateObjInConfigMap(h.scheme, instance, mfs.KubeletConfig, profile.Name, hypershiftconsts.ConfigKey, ntoGeneratedMachineConfigLabel)
		if err != nil {
			return err
		}
		err = createOrUpdateKubeletConfigConfigMap(ctx, h.controlPlaneClient, cm)
		if err != nil {
			return err
		}
	}

	if performanceTunedMutated != nil {
		cm, err := EncapsulateObjInConfigMap(h.scheme, instance, mfs.Tuned, profile.Name, hypershiftconsts.TuningKey, tunedConfigMapLabel)
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

func (h *handler) getContainerRuntimeName(ctx context.Context, profile *performancev2.PerformanceProfile) (mcov1.ContainerRuntimeDefaultRuntime, error) {
	cmList := &corev1.ConfigMapList{}
	if err := h.controlPlaneClient.List(ctx, cmList, &client.ListOptions{
		Namespace: ntoconfig.OperatorNamespace(),
	}); err != nil {
		return "", err
	}
	var ctrcfgs []*mcov1.ContainerRuntimeConfig
	for _, cm := range cmList.Items {
		data, ok := cm.Data[hypershiftconsts.ConfigKey]
		// container runtime config should be store in the Config key
		if !ok {
			continue
		}
		// ConfigMaps with the PerformanceProfileNameLabel label are generated by
		// the controller itself
		if _, ok = cm.Labels[hypershift.PerformanceProfileNameLabel]; ok {
			continue
		}
		ctrcfg, err := validateAndExtractContainerRuntimeConfigFrom(h.scheme, []byte(data))
		if err != nil {
			return "", fmt.Errorf("failed to get ContainerRuntime name %w", err)
		}
		if ctrcfg != nil {
			ctrcfgs = append(ctrcfgs, ctrcfg)
		}
	}
	if len(ctrcfgs) == 0 {
		klog.V(1).Infof("no ContainerRuntimeConfig found that associated with performance profile %q; using default container runtime %q", profile.Name, mcov1.ContainerRuntimeDefaultRuntimeRunc)
		return mcov1.ContainerRuntimeDefaultRuntimeRunc, nil
	}
	if len(ctrcfgs) > 1 {
		return "", fmt.Errorf("more than one ContainerRuntimeConfig found that associated with performance profile %q", profile.Name)
	}
	// Ideally, the controller is supposed to check the ContainerRuntimeConfig status and return the value only if
	// ContainerRuntimeConfig applied successfully.
	// On hypershift, the controller does not have access to the status, so it would have to relay on hypershift operator
	// to apply the ContainerRuntimeConfig configuration correctly.
	// In case something goes wrong with the ContainerRuntimeConfig application,
	// the hypershift operator won't be applying NTO controller's configuration either
	// and will reflect that on the NodePool's status.
	return ctrcfgs[0].Spec.ContainerRuntimeConfig.DefaultRuntime, nil
}

func EncapsulateObjInConfigMap(scheme *runtime.Scheme, instance *corev1.ConfigMap, object client.Object, profileName, dataKey, objectLabel string) (*corev1.ConfigMap, error) {
	encodedObj, err := hypershift.EncodeManifest(object, scheme)
	if err != nil {
		return nil, err
	}
	nodePoolNamespacedName, ok := instance.Annotations[hypershiftNodePoolLabel]
	if !ok {
		return nil, fmt.Errorf("annotation %q not found in ConfigMap %q annotations", hypershiftNodePoolLabel, client.ObjectKeyFromObject(instance).String())
	}

	name := fmt.Sprintf("%s-%s", strings.ToLower(object.GetObjectKind().GroupVersionKind().Kind), instance.Name)
	cm := configMapMeta(name, profileName, instance.GetNamespace(), nodePoolNamespacedName)
	err = controllerutil.SetControllerReference(instance, cm, scheme)
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

// configMapMeta return a ConfigMap that can be used to encapsulate
// cluster scoped objects within the desired Namespace
func configMapMeta(name, profileName, namespace, npNamespacedName string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				hypershift.PerformanceProfileNameLabel: profileName,
				hypershiftNodePoolLabel:                parseNamespacedName(npNamespacedName),
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

func validateAndExtractContainerRuntimeConfigFrom(scheme *runtime.Scheme, manifest []byte) (*mcov1.ContainerRuntimeConfig, error) {
	ctrcfg := &mcov1.ContainerRuntimeConfig{}
	ok, err := hypershift.DecodeManifest(manifest, scheme, ctrcfg)
	if !ok {
		return nil, err
	}
	return ctrcfg, err
}
