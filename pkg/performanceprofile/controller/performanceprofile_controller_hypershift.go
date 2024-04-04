/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	performanceprofilecomponents "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/runtimeclass"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	hypershiftPerformanceProfileNameLabel = "hypershift.openshift.io/performanceProfileName"
	hypershiftNodePoolNameLabel           = "hypershift.openshift.io/nodePoolName"
	hypershiftNodePoolLabel               = "hypershift.openshift.io/nodePool"
	controllerGeneratedMachineConfig      = "hypershift.openshift.io/performanceprofile-config"

	tunedConfigMapLabel     = "hypershift.openshift.io/tuned-config"
	tunedConfigMapConfigKey = "tuning"

	mcoConfigMapConfigKey          = "config"
	ntoGeneratedMachineConfigLabel = "hypershift.openshift.io/nto-generated-machine-config"

	hypershiftFinalizer = "hypershift.openshift.io/foreground-deletion"
)

func configureScheme(scheme *runtime.Scheme) error {
	if err := tunedv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("unable to add tuned/v1 to scheme. %w", err)
	}
	if err := mcfgv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("unable to add machineconfiguration.openshift.io/v1 to scheme. %w", err)
	}
	if err := performancev2.AddToScheme(scheme); err != nil {
		return fmt.Errorf("unable to add performanceprofile/v2 to scheme. %w", err)
	}
	return nil
}

func (r *PerformanceProfileReconciler) HypershiftSetupWithManager(mgr ctrl.Manager, managementCluster cluster.Cluster) error {
	// In hypershift just have to reconcile ConfigMaps created by Hypershift Operator in the
	// controller namespace with the right label.
	p := predicate.Funcs{
		UpdateFunc: func(ue event.UpdateEvent) bool {
			if !validateUpdateEvent(&ue) {
				klog.InfoS("UpdateEvent NOT VALID", "objectName", ue.ObjectOld.GetName())
				return false
			}

			_, hasLabel := ue.ObjectNew.GetLabels()[controllerGeneratedMachineConfig]
			if hasLabel {
				klog.InfoS("UpdateEvent has label", "objectName", ue.ObjectOld.GetName(), "label", controllerGeneratedMachineConfig)
			}
			return hasLabel
		},
		CreateFunc: func(ce event.CreateEvent) bool {
			if ce.Object == nil {
				klog.Error("Create event has no runtime object")
				return false
			}

			_, hasLabel := ce.Object.GetLabels()[controllerGeneratedMachineConfig]
			if hasLabel {
				klog.InfoS("CreateEvent has label", "objectName", ce.Object.GetName(), "label", controllerGeneratedMachineConfig)
			}
			return hasLabel
		},
		DeleteFunc: func(de event.DeleteEvent) bool {
			if de.Object == nil {
				klog.Error("Delete event has no runtime object")
				return false
			}
			_, hasLabel := de.Object.GetLabels()[controllerGeneratedMachineConfig]
			if hasLabel {
				klog.InfoS("DeleteEvent has label", "objectName", de.Object.GetName(), "label", controllerGeneratedMachineConfig)
			}
			return hasLabel
		},
	}

	if err := configureScheme(r.Scheme); err != nil {
		klog.ErrorS(err, "unable to configure scheme")
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("performanceprofile_controller").
		WatchesRawSource(source.Kind(managementCluster.GetCache(), &corev1.ConfigMap{}),
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(p)).Complete(r)
}

func (r *PerformanceProfileReconciler) hypershiftReconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.InfoS("*** Entering ReconcileLoop ***", "reqNamespace", req.NamespacedName)
	defer klog.InfoS("*** Exiting ReconcileLoop ***", "reqNamespace", req.NamespacedName)

	instance := &corev1.ConfigMap{}

	err := r.ManagementClient.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			klog.InfoS("Error: Instance not found", "reqNamespace", req.NamespacedName)
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		klog.ErrorS(err, "Reading failure", "reqNamespace", req.NamespacedName)
		return reconcile.Result{}, err
	}

	if instance.DeletionTimestamp != nil {
		klog.InfoS("deletion timestamp NOT NULL ", "instanceName", instance.Name)
		// ConfigMap is marked for deletion and waiting for finalizers to be empty
		// so better to clean-up and delete the objects.
		if err := hypershiftDeleteComponents(r.Client, ctx, instance); err != nil {
			klog.ErrorS(err, "failed to delete components.", "reqNamespace", req.NamespacedName)
			r.Recorder.Eventf(instance, corev1.EventTypeWarning, "Deletion failed", "[hypershift:%s] Failed to delete components: %v", req.NamespacedName, err)
			return reconcile.Result{}, err
		}
		r.Recorder.Eventf(instance, corev1.EventTypeNormal, "Deletion succeeded", "[hypershift: %s] Succeeded to delete all components", req.NamespacedName)

		// remove finalizer
		if configMapHasFinalizer(instance, hypershiftFinalizer) {
			klog.InfoS("Has finalizer. Removing ... ", "instanceName", instance.Name)
			cm := configMapRemoveFinalizer(instance, hypershiftFinalizer)
			if err := r.ManagementClient.Update(ctx, cm); err != nil {
				klog.ErrorS(err, "error while trying to update configmap", "instanceName", instance.Name)
				return reconcile.Result{}, err
			}
			klog.InfoS("Configmap updated, finalizer deleted", "instanceName", instance.Name)
			return reconcile.Result{}, nil
		}
	}

	//add finalizer
	if !configMapHasFinalizer(instance, hypershiftFinalizer) {
		klog.InfoS("[%s] Do NOT has finalizer. Adding ... ", instance.Name)
		instance.Finalizers = append(instance.Finalizers, hypershiftFinalizer)
		if err := r.ManagementClient.Update(ctx, instance); err != nil {
			klog.ErrorS(err, "error while trying to update configmap.", "instanceName", instance.Name)
			return reconcile.Result{}, err
		}
		klog.InfoS("Configmap updated, finalizer added", "instanceName", instance.Name)
		return reconcile.Result{}, nil
	}

	performanceProfileString, ok := instance.Data[tunedConfigMapConfigKey]
	if !ok {
		klog.ErrorS(err, "ConfigMap has no PerformanceProfile info inside", "instanceName", instance.Name, "tunedConfigMapConfigKey", tunedConfigMapConfigKey)
		return reconcile.Result{}, fmt.Errorf("configmap %q has no PerformanceProfile info inside (no entry %q)", instance.Name, tunedConfigMapConfigKey)
	}

	cmNodePoolNamespacedName, ok := instance.Annotations[hypershiftNodePoolLabel]
	if !ok {
		klog.ErrorS(err, "ConfigMap has no Annotation", "instanceName", instance.Name, "annotation", hypershiftNodePoolLabel)
		// Return and don't requeue
		return reconcile.Result{}, nil
	}
	nodePoolName := parseNamespacedName(cmNodePoolNamespacedName)

	performanceProfileFromConfigMap, err := parsePerformanceProfileManifest([]byte(performanceProfileString), nodePoolName, r.Scheme)
	if err != nil {
		klog.ErrorS(err, "failed to parse PerformanceProfile manifest from Configmap data", "instanceName", instance.Name)
		// Return and don't requeue
		return reconcile.Result{}, fmt.Errorf("failed to parse PerformanceProfile manifest from Configmap %q data: %w", instance.Name, err)
	}

	updatePerformanceProfileName(performanceProfileFromConfigMap, nodePoolName)
	klog.InfoS("PerformanceProfile name updated to", "instanceName", instance.Name, "ppName", performanceProfileFromConfigMap.Name)

	pinningMode, err := r.getInfraPartitioningMode()
	if err != nil {
		return ctrl.Result{}, err
	}

	//ContainerRuntimeConfiguration is not yet supported in Hypershift
	// see : https://github.com/openshift/hypershift/blob/1586b54b9ea0b60ecdd5e4d4fb57f99da51b581d/hypershift-operator/controllers/nodepool/nodepool_controller.go#L1962
	// so we gonna go with the default Runtime for now
	//TODO - Get ContainerRuntimeConfiguration when supported by Hypershift
	//NOTE - ContainerRuntimeConfiguration is intended to be listed in NodePools `spec.config` field as a ConfigMap reference
	//       so the way to read it could be difficult unless we could use some other way
	var ctrRuntime mcov1.ContainerRuntimeDefaultRuntime = mcov1.ContainerRuntimeDefaultRuntimeDefault
	klog.InfoS("hypershift ContainerRuntimeConfig is not supported for Hypershift yet. Using default", "instanceName", instance.Name, "defaultRuntime", ctrRuntime)

	componentSet, err := manifestset.GetNewComponents(performanceProfileFromConfigMap,
		&performanceprofilecomponents.Options{
			ProfileMCP: nil,
			MachineConfig: performanceprofilecomponents.MachineConfigOptions{
				PinningMode:    &pinningMode,
				DefaultRuntime: ctrRuntime},
		})

	if err != nil {
		klog.ErrorS(err, "error while getting componentset", "instanceName", instance.Name)
		r.Recorder.Eventf(instance, corev1.EventTypeWarning, "Creation failed", "[hypershift:%s] Failed to create all components: %v", instance.Name, err)
		return reconcile.Result{}, fmt.Errorf("unable to get components from PerformanceProfile in ConfigMap %q. %w", instance.Name, err)
	}

	// Now we have to create a ConfigMap for each of the elements in the componentSet and then handle them to
	// the different agents that would made them effective in the managed cluster.( where workers are)
	tunedEncoded, err := encodeManifest(componentSet.Tuned, r.Scheme)
	if err != nil {
		klog.ErrorS(err, "failed to encode TuneD", "instanceName", instance.Name)
		return reconcile.Result{}, fmt.Errorf("failed to encode TuneD from %q. %w", instance.Name, err)
	}

	tunedConfigMap := TunedConfigMap(instance, performanceProfileFromConfigMap.Name, cmNodePoolNamespacedName, string(tunedEncoded))
	if err := createOrUpdateTunedConfigMap(tunedConfigMap, ctx, r.ManagementClient); err != nil {
		klog.ErrorS(err, "error while creating/updating tuned configmap", "instanceName", instance.Name, "tunedConfigMapName", tunedConfigMap.Name)
		return reconcile.Result{}, fmt.Errorf("unable to create/update tuned configmap %q from %q. %w", tunedConfigMap.Name, instance.Name, err)
	}

	machineconfigEncoded, err := encodeManifest(convertMachineConfig(componentSet.MachineConfig), r.Scheme)
	if err != nil {
		klog.ErrorS(err, "error while encoding MachineConfig", "instanceName", instance.Name)
		return reconcile.Result{}, fmt.Errorf("unable to encode MachineConfig from %q. %w", instance.Name, err)
	}

	machineconfigConfigMap := MachineConfigConfigMap(instance, performanceProfileFromConfigMap.Name, cmNodePoolNamespacedName, string(machineconfigEncoded))
	if err := createOrUpdateMachineConfigConfigMap(machineconfigConfigMap, ctx, r.ManagementClient); err != nil {
		klog.ErrorS(err, "error while creating/updating machineconfig configmap", "instanceName", instance.Name, "mcConfigMapName", machineconfigConfigMap.Name)
		return reconcile.Result{}, fmt.Errorf("unable to create/update machineconfig configmap %q from %q. %w", machineconfigConfigMap.Name, instance.Name, err)
	}

	kubeletconfigEncoded, err := encodeManifest(convertKubeletConfig(componentSet.KubeletConfig), r.Scheme)
	if err != nil {
		klog.ErrorS(err, "error while encoding kubeletconfig", "instanceName", instance.Name)
		return reconcile.Result{}, fmt.Errorf("unable to encode kubeletconfig from %q. %w", instance.Name, err)
	}

	kubeletconfigConfigMap := KubeletConfigConfigMap(instance, performanceProfileFromConfigMap.Name, cmNodePoolNamespacedName, string(kubeletconfigEncoded))
	if err := createOrUpdateKubeletConfigConfigConfigMap(kubeletconfigConfigMap, ctx, r.ManagementClient); err != nil {
		klog.ErrorS(err, "error while creating/updating kubeletconfig configmap", "instanceName", instance.Name, "kcConfigMapName", kubeletconfigConfigMap.Name)
		return reconcile.Result{}, fmt.Errorf("unable to create/update kubeletconfig configmap %q from %q. %w", kubeletconfigConfigMap.Name, instance.Name, err)
	}

	componentSet.RuntimeClass.Name = runtimeclass.BuildRuntimeClassName(instance.Name)
	if err := createOrUpdateRuntimeClass(r.Client, ctx, componentSet.RuntimeClass); err != nil {
		klog.ErrorS(err, "error while creating/updating runtimeclass", "instanceName", instance.Name, "rtclassName", componentSet.RuntimeClass.Name)
		return reconcile.Result{}, fmt.Errorf("unable to create/update runtimeclass %q from %q. %w", componentSet.RuntimeClass.Name, instance.Name, err)
	}
	klog.InfoS("Processed ok", "instanceName", instance.Name)
	r.Recorder.Eventf(instance, corev1.EventTypeNormal, "Creation succeeded", "[hypershift:%s] Succeeded to create all components", instance.Name)
	return reconcile.Result{}, nil
}

func readRuntimeClass(cli client.Client, ctx context.Context, name string) (*nodev1.RuntimeClass, error) {
	rtClass := &nodev1.RuntimeClass{}

	key := types.NamespacedName{
		Name: name,
	}

	if err := cli.Get(ctx, key, rtClass); err != nil {
		return nil, err
	}
	return rtClass, nil
}

func encodeManifest(obj runtime.Object, scheme *runtime.Scheme) ([]byte, error) {
	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)

	buff := bytes.Buffer{}
	err := yamlSerializer.Encode(obj, &buff)
	return buff.Bytes(), err
}

func createOrUpdateTunedConfigMap(cm *corev1.ConfigMap, ctx context.Context, cli client.Client) error {
	tunedConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) error {
		dst.Data[tunedConfigMapConfigKey] = orig.Data[tunedConfigMapConfigKey]
		return nil
	}
	return createOrUpdateConfigMap(ctx, cli, cm, tunedConfigMapUpdateFunc)
}

func createOrUpdateMachineConfigConfigMap(cm *corev1.ConfigMap, ctx context.Context, cli client.Client) error {
	machineconfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) error {
		dst.Data[mcoConfigMapConfigKey] = orig.Data[mcoConfigMapConfigKey]
		return nil
	}
	return createOrUpdateConfigMap(ctx, cli, cm, machineconfigConfigMapUpdateFunc)
}

func createOrUpdateKubeletConfigConfigConfigMap(cm *corev1.ConfigMap, ctx context.Context, cli client.Client) error {
	kubeletconfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) error {
		dst.Data[mcoConfigMapConfigKey] = orig.Data[mcoConfigMapConfigKey]
		return nil
	}
	return createOrUpdateConfigMap(ctx, cli, cm, kubeletconfigConfigMapUpdateFunc)
}

func createOrUpdateConfigMap(ctx context.Context, cli client.Client, cm *corev1.ConfigMap, updateFunc func(origin, destination *corev1.ConfigMap) error) error {
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
		if err := updateFunc(cm, tcm); err != nil {
			return fmt.Errorf("failed while updateing configmap content %q: %w", cm.Name, err)
		}
		if err := cli.Update(ctx, tcm); err != nil {
			return fmt.Errorf("failed to update configmap %q: %w", cm.Name, err)
		}
	}
	return nil
}

func newOwnerReference(owner metav1.Object, gvk schema.GroupVersionKind) metav1.OwnerReference {
	blockOwnerDeletion := false
	isController := false
	return metav1.OwnerReference{
		APIVersion:         gvk.GroupVersion().String(),
		Kind:               gvk.Kind,
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &isController,
	}
}

func createOrUpdateRuntimeClass(cli client.Client, ctx context.Context, rtClass *nodev1.RuntimeClass) error {
	existing, err := readRuntimeClass(cli, ctx, rtClass.Name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			err := cli.Create(ctx, rtClass)
			return err
		}
	}

	mutated := existing.DeepCopy()
	mergeMaps(rtClass.Annotations, mutated.Annotations)
	mergeMaps(rtClass.Labels, mutated.Labels)
	mutated.Handler = rtClass.Handler
	mutated.Scheduling = rtClass.Scheduling

	// we do not need to update if it no change between mutated and existing object
	if apiequality.Semantic.DeepEqual(existing.Handler, mutated.Handler) &&
		apiequality.Semantic.DeepEqual(existing.Scheduling, mutated.Scheduling) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil
	}

	err = cli.Update(ctx, mutated, &client.UpdateOptions{})
	return err
}

func TunedConfigMap(owner *corev1.ConfigMap, performanceProfileName, nodePoolNamespacedName, tunedManifest string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: owner.GetNamespace(),
			Name:      "tuned-" + owner.Name,
			Labels: map[string]string{
				tunedConfigMapLabel:                   "true",
				hypershiftPerformanceProfileNameLabel: performanceProfileName,
				hypershiftNodePoolLabel:               parseNamespacedName(nodePoolNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftNodePoolLabel: nodePoolNamespacedName,
			},
			OwnerReferences: []metav1.OwnerReference{
				newOwnerReference(owner, owner.GroupVersionKind()),
			},
		},
		Data: map[string]string{
			tunedConfigMapConfigKey: tunedManifest,
		},
	}
}

func MachineConfigConfigMap(owner *corev1.ConfigMap, performanceProfileName string, nodePoolNamespacedName string, machineconfigManifest string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: owner.GetNamespace(),
			Name:      "mc-" + owner.Name,
			Labels: map[string]string{
				ntoGeneratedMachineConfigLabel:        "true",
				hypershiftPerformanceProfileNameLabel: performanceProfileName,
				hypershiftNodePoolLabel:               parseNamespacedName(nodePoolNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftNodePoolLabel: nodePoolNamespacedName,
			},
			OwnerReferences: []metav1.OwnerReference{
				newOwnerReference(owner, owner.GroupVersionKind()),
			},
		},
		Data: map[string]string{
			mcoConfigMapConfigKey: machineconfigManifest,
		},
	}
}

func KubeletConfigConfigMap(owner *corev1.ConfigMap, performanceProfileName string, nodePoolNamespacedName string, kubeletconfigManifest string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: owner.GetNamespace(),
			Name:      "kc-" + owner.Name,
			Labels: map[string]string{
				ntoGeneratedMachineConfigLabel:        "true",
				hypershiftPerformanceProfileNameLabel: performanceProfileName,
				hypershiftNodePoolLabel:               parseNamespacedName(nodePoolNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftNodePoolLabel: nodePoolNamespacedName,
			},
			OwnerReferences: []metav1.OwnerReference{
				newOwnerReference(owner, owner.GroupVersionKind()),
			},
		},
		Data: map[string]string{
			mcoConfigMapConfigKey: kubeletconfigManifest,
		},
	}
}

func convertMachineConfig(origMC *mcov1.MachineConfig) *mcfgv1.MachineConfig {
	return &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: origMC.ObjectMeta,
		// althoug mco.MC.Spec and hypershift mco.MC.Spec are defined in
		// different files they have the same structure, so the conversion is
		// almost direct ( but for the BaseOSExtensionsContainerImage)
		Spec: mcfgv1.MachineConfigSpec{
			OSImageURL:      origMC.Spec.OSImageURL,
			Config:          origMC.Spec.Config,
			KernelArguments: origMC.Spec.KernelArguments,
			Extensions:      origMC.Spec.Extensions,
			FIPS:            origMC.Spec.FIPS,
			KernelType:      origMC.Spec.KernelType,
		},
	}
}

func convertKubeletConfig(origKC *mcov1.KubeletConfig) *mcfgv1.KubeletConfig {
	return &mcfgv1.KubeletConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "KubeletConfig",
		},
		ObjectMeta: origKC.ObjectMeta,
		//NOTE - MachineConfigPoolSelector left empty as NodePool is the one
		// that relates nodes with MachineConfigs in hypershift.
		Spec: mcfgv1.KubeletConfigSpec{
			KubeletConfig: origKC.Spec.KubeletConfig,
		},
	}
}

// parseManifests parses a YAML or JSON document that may contain one or more
// kubernetes resources.
func parsePerformanceProfileManifest(data []byte, nodePoolName string, scheme *runtime.Scheme) (*performancev2.PerformanceProfile, error) {
	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)

	cr, _, err := yamlSerializer.Decode(data, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error decoding PerformanceProfile manifests: %v", err)
	}
	performanceProfile, ok := cr.(*performancev2.PerformanceProfile)
	if !ok {
		return nil, fmt.Errorf("error parsing PerformanceProfile manifests")
	}
	return performanceProfile, nil
}

func updatePerformanceProfileName(performanceProfile *performancev2.PerformanceProfile, nodePoolName string) {
	// Make PerformanceProfile names unique if a PerformanceProfile is duplicated across NodePools
	// for example, if one ConfigMap is referenced in multiple NodePools
	if !strings.HasSuffix(performanceProfile.ObjectMeta.Name, "-"+hashStruct(nodePoolName)) {
		performanceProfile.SetName(performanceProfile.ObjectMeta.Name + "-" + hashStruct(nodePoolName))
	}
}

func hypershiftDeleteComponents(remoteClient client.Client, ctx context.Context, ppConfigMap *corev1.ConfigMap) error {
	// ConfigMap is marked for deletion and waiting for finalizers to be empty
	// so better to clean-up and delete the objects.

	// just delete RtClass, as right now all the other components created from this PP
	// are embedded into ConfigMaps which has an OwnerReference with the PP configmap
	// and will be deleted by k8s machinery trigerring the deletion procedure of the
	// embedded elements.
	rtName := runtimeclass.BuildRuntimeClassName(ppConfigMap.Name)
	rtClass, err := readRuntimeClass(remoteClient, ctx, rtName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// rtClass not found so nothing to delete, so delete process has finished
			return nil
		}
		return fmt.Errorf("unable to read RuntimeClass %q, error: %w. Unable to finalize deletion procedure properly", rtName, err)
	}

	err = remoteClient.Delete(ctx, rtClass)
	return err
}

func configMapHasFinalizer(cm *corev1.ConfigMap, finalizer string) bool {
	for _, f := range cm.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

func configMapRemoveFinalizer(cm *corev1.ConfigMap, finalizer string) *corev1.ConfigMap {
	var finalizers []string
	for _, finalizer := range cm.Finalizers {
		if finalizer != hypershiftFinalizer {
			finalizers = append(finalizers, finalizer)
		}
	}
	cm.Finalizers = finalizers

	return cm
}

func hashStruct(o interface{}) string {
	hash := fnv.New32a()
	hash.Write([]byte(fmt.Sprintf("%v", o)))
	intHash := hash.Sum32()
	return fmt.Sprintf("%08x", intHash)
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
