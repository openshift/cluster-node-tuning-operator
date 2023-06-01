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

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/klog/v2"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// REVIEW - Most of these already are defined in 'pkg/operator/hypershift.go' maybe we should avoid duplication.
//
//	What about creating a file with all these constants and labels and reference them both here and in the
//	NTO hypershift code?
//
// REVIEW - Reorder constants to read them better.
const (
	hypershiftPerformanceProfileNameLabel = "hypershift.openshift.io/performanceProfileName"
	hypershiftNodePoolNameLabel           = "hypershift.openshift.io/nodePoolName"
	hypershiftNodePoolLabel               = "hypershift.openshift.io/nodePool"
	controllerGeneratedMachineConfig      = "hypershift.openshift.io/performanceprofile-config"

	tunedConfigMapLabel     = "hypershift.openshift.io/tuned-config"
	tunedConfigMapConfigKey = "tuning"

	mcoConfigMapConfigKey          = "config"
	ntoGeneratedMachineConfigLabel = "hypershift.openshift.io/nto-generated-machine-config"
)

func (r *PerformanceProfileReconciler) hypershiftSetupWithManager(mgr ctrl.Manager) error {

	if !ntconfig.InHyperShift() {
		return fmt.Errorf("Using hypershift controller configuration while not in hypershift deployment")
	}

	// In hypershift just have to reconcile ConfigMaps created by Hypershift Operator in the
	// controller namespace with the right label.
	p := predicate.Funcs{
		UpdateFunc: func(ue event.UpdateEvent) bool {
			if !validateUpdateEvent(&ue) {
				return false
			}

			if _, ok := ue.ObjectNew.GetLabels()[controllerGeneratedMachineConfig]; ok {
				return ue.ObjectOld.GetGeneration() != ue.ObjectNew.GetGeneration()
			}
			return false
		},
		CreateFunc: func(ce event.CreateEvent) bool {
			if ce.Object == nil {
				klog.Error("Create event has no runtime object")
				return false
			}

			_, hasLabel := ce.Object.GetLabels()[controllerGeneratedMachineConfig]
			return hasLabel
		},
		DeleteFunc: func(de event.DeleteEvent) bool {
			if de.Object == nil {
				klog.Error("Delete event has no runtime object")
				return false
			}

			_, hasLabel := de.Object.GetLabels()[controllerGeneratedMachineConfig]
			return hasLabel
		},
	}

	return ctrl.NewControllerManagedBy(mgr).For(&corev1.ConfigMap{}, builder.WithPredicates(p)).Complete(r)
}

func (r *PerformanceProfileReconciler) hypershiftReconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	if !ntconfig.InHyperShift() {
		return reconcile.Result{}, fmt.Errorf("Using hypershift controller configuration while not in hypershift deployment")
	}

	instance := &corev1.ConfigMap{}

	//REVIEW - Should be OperatorNamespace?
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			//FIXME - In hypershift there is no "owned" objects. So how do we delete created objects? or do we even have to?
			//jlom - as we create a configmap for each of the elements created from a PerformanceProfile and we reference
			//       these configmaps in NodePool so they could be "transfered" to the managed cluster, maybe "delete" them is
			//       as simple as derreference them in NodePool and delete the ConfigMaps.
			//NOTE - Adding a label with PerformanceProfile name on it to the configmaps could make this look up easier.

			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	performanceProfileString, ok := instance.Data[tunedConfigMapConfigKey]
	if !ok {
		klog.Warning("ConfigMap %s has no data for field %s", instance.ObjectMeta.Name, tunedConfigMapConfigKey)

		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	cmNodePoolNamespacedName, ok := instance.Annotations[hypershiftNodePoolLabel]
	if !ok {
		klog.Warningf("failed to parse PerformanceProfileManifests in ConfigMap %s, no label %s", instance.ObjectMeta.Name, hypershiftNodePoolLabel)
		// Return and don't requeue
		return reconcile.Result{}, nil
	}
	nodePoolName := parseNamespacedName(cmNodePoolNamespacedName)

	performanceProfileFromConfigMap, err := parsePerformanceProfileManifest([]byte(performanceProfileString), nodePoolName)
	if err != nil {
		klog.Warningf("failed to parseTunedManifests in ConfigMap %s: %v", instance.ObjectMeta.Name, err)
		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	if err := updatePerformanceProfile(performanceProfileFromConfigMap, ctx, r.Client, instance, nodePoolName); err != nil {
		klog.Errorf("failed to update performance profile from configMap %q components: %v", instance.Name, err)
		//REVIEW - Should we record this events? and if so where? in the CM or the PP?
		return reconcile.Result{}, err
	}

	pinningMode, err := r.getInfraPartitioningMode()
	if err != nil {
		return ctrl.Result{}, err
	}

	//REVIEW - jlom Not fully sure if `getContinerRuntimeName` gonna work in hypershift env
	ctrRuntime, err := r.getContainerRuntimeName(ctx, performanceProfileFromConfigMap)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("hypershift could not determine high-performance runtime class container-runtime for profile %q; %w", performanceProfileFromConfigMap.Name, err)
	}
	klog.Infof("hypershift using %q as high-performance runtime class container-runtime for profile %q", ctrRuntime, performanceProfileFromConfigMap.Name)

	componentSet, err := manifestset.GetNewComponents(performanceProfileFromConfigMap, nil, &pinningMode, ctrRuntime)
	if err != nil {
		klog.Errorf("failed to deploy performance profile from configMap %q components: %v", instance.Name, err)
		//REVIEW - Should we record this events? and if so where? in the CM or the PP?
		return reconcile.Result{}, err
	}

	//jlom - Now we have to create a ConfigMap for each of the elements in the componentSet and then handle them to
	//       the different agents that would made them effective in the managed cluster.( where workers are)
	tunedEncoded, err := encodeTuned(componentSet.Tuned)
	if err != nil {
		klog.Warningf("failed to Encode Tuned in ConfigMap %s: %v", instance.ObjectMeta.Name, err)
		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	tunedConfigMap := TunedConfigMap(instance.Namespace, performanceProfileFromConfigMap.Name, cmNodePoolNamespacedName, string(tunedEncoded))

	if err := createOrUpdateTunedConfigMap(tunedConfigMap, ctx, r.Client); err != nil {
		klog.Error("failure on Tuned process: %w", err.Error())
		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	machineconfigEncoded, err := encodeMachineConfig(convertMachineConfig(componentSet.MachineConfig))
	if err != nil {
		klog.Warningf("failed to Encode MachineConfig in ConfigMap %s: %v", instance.ObjectMeta.Name, err)
		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	machineconfigConfigMap := MachineConfigConfigMap(instance.Namespace, performanceProfileFromConfigMap.Name, cmNodePoolNamespacedName, string(machineconfigEncoded))

	if err := createOrUpdateMachineConfigConfigMap(machineconfigConfigMap, ctx, r.Client); err != nil {
		klog.Error("failure on MachineConfig process: %w", err.Error())
		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	kubeletconfigEncoded, err := encodeKubeletConfig(convertKubeletConfig(componentSet.KubeletConfig))
	if err != nil {
		klog.Warningf("failed to Encode KubeletConfig in ConfigMap %s: %v", instance.ObjectMeta.Name, err)
		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	kubeletconfigConfigMap := KubeletConfigConfigMap(instance.Namespace, performanceProfileFromConfigMap.Name, cmNodePoolNamespacedName, string(kubeletconfigEncoded))

	if err := createOrUpdateKubeletConfigConfigConfigMap(kubeletconfigConfigMap, ctx, r.Client); err != nil {
		klog.Error("failure on KubeletConfig process: %w", err.Error())
		// Return and don't requeue
		return reconcile.Result{}, nil
	}

	return reconcile.Result{}, nil
}

func encodePerformanceProfile(performanceProfile *performancev2.PerformanceProfile) ([]byte, error) {
	scheme := runtime.NewScheme()
	performancev2.AddToScheme(scheme)
	performanceProfileEncoded, err := encodeManifest(performanceProfile, scheme)
	return performanceProfileEncoded, err
}

func encodeTuned(tuned *tunedv1.Tuned) ([]byte, error) {
	scheme := runtime.NewScheme()
	tunedv1.AddToScheme(scheme)
	tunedEncoded, err := encodeManifest(tuned, scheme)
	return tunedEncoded, err
}

func encodeMachineConfig(machineConfig *mcfgv1.MachineConfig) ([]byte, error) {
	scheme := runtime.NewScheme()
	mcfgv1.AddToScheme(scheme)
	mcEncoded, err := encodeManifest(machineConfig, scheme)
	return mcEncoded, err
}

func encodeKubeletConfig(kubeletConfig *mcfgv1.KubeletConfig) ([]byte, error) {
	scheme := runtime.NewScheme()
	mcfgv1.AddToScheme(scheme)
	mcEncoded, err := encodeManifest(kubeletConfig, scheme)
	return mcEncoded, err
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
		//REVIEW - Maybe here I should ensure the readed ConfifMap has the needed labels and annotations.
		dst.Data[tunedConfigMapConfigKey] = orig.Data[tunedConfigMapConfigKey]
		return nil
	}
	return createOrUpdateConfigMap(ctx, cli, cm, tunedConfigMapUpdateFunc)
}

func createOrUpdateMachineConfigConfigMap(cm *corev1.ConfigMap, ctx context.Context, cli client.Client) error {
	machineconfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) error {
		//REVIEW - Maybe here I should ensure the readed ConfifMap has the needed labels and annotations.
		dst.Data[mcoConfigMapConfigKey] = orig.Data[mcoConfigMapConfigKey]
		return nil
	}
	return createOrUpdateConfigMap(ctx, cli, cm, machineconfigConfigMapUpdateFunc)
}

func createOrUpdateKubeletConfigConfigConfigMap(cm *corev1.ConfigMap, ctx context.Context, cli client.Client) error {
	kubeletconfigConfigMapUpdateFunc := func(orig, dst *corev1.ConfigMap) error {
		//REVIEW - Maybe here I should ensure the readed ConfifMap has the needed labels and annotations.
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

func TunedConfigMap(namespace, performanceProfileName, nodePoolNamespacedName, tunedManifest string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("tuned-%s", performanceProfileName),
			Labels: map[string]string{
				tunedConfigMapLabel:                   "true",
				hypershiftPerformanceProfileNameLabel: performanceProfileName,
				hypershiftNodePoolLabel:               parseNamespacedName(nodePoolNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftNodePoolLabel: nodePoolNamespacedName,
			},
		},
		Data: map[string]string{
			tunedConfigMapConfigKey: tunedManifest,
		},
	}
}

func MachineConfigConfigMap(namespace string, performanceProfileName string, nodePoolNamespacedName string, machineconfigManifest string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("mc-%s", performanceProfileName),
			Labels: map[string]string{
				ntoGeneratedMachineConfigLabel:        "true",
				hypershiftPerformanceProfileNameLabel: performanceProfileName,
				hypershiftNodePoolLabel:               parseNamespacedName(nodePoolNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftNodePoolLabel: nodePoolNamespacedName,
			},
		},
		Data: map[string]string{
			mcoConfigMapConfigKey: machineconfigManifest,
		},
	}
}

func KubeletConfigConfigMap(namespace string, performanceProfileName string, nodePoolNamespacedName string, kubeletconfigManifest string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("kc-%s", performanceProfileName),
			Labels: map[string]string{
				ntoGeneratedMachineConfigLabel:        "true",
				hypershiftPerformanceProfileNameLabel: performanceProfileName,
				hypershiftNodePoolLabel:               parseNamespacedName(nodePoolNamespacedName),
			},
			Annotations: map[string]string{
				hypershiftNodePoolLabel: nodePoolNamespacedName,
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
		// direct
		//TODO - Is there any way to do this conversion in a type safe manner?
		Spec: mcfgv1.MachineConfigSpec(origMC.Spec),
	}
}

func convertKubeletConfig(origKC *mcov1.KubeletConfig) *mcfgv1.KubeletConfig {

	return &mcfgv1.KubeletConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "KubeletConfig",
		},
		ObjectMeta: origKC.ObjectMeta,
		//TODO - Is there any way to do this conversion in a type safe manner?
		//NOTE - MachineConfigPoolSelector left empty as NodePool is the one
		// that relates nodes with MachineConfigs in hypershift.
		Spec: mcfgv1.KubeletConfigSpec{
			KubeletConfig: origKC.Spec.KubeletConfig,
		},
	}
}

// parseManifests parses a YAML or JSON document that may contain one or more
// kubernetes resources.
func parsePerformanceProfileManifest(data []byte, nodePoolName string) (*performancev2.PerformanceProfile, error) {
	scheme := runtime.NewScheme()
	performancev2.AddToScheme(scheme)

	//REVIEW - As serializer is used many times it could be worthy to create it just one time and keep it somewhere
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

// Update PerformanceProfile Manifest so:
// - Name is unique and a function of the original PerformanceProfile and NodePoolName
// - Add label: "hypershift.openshift.io/nodePoolName" : <nodePoolName> to reference the NodePool where this PerformanceProfile is referenced
func updatePerformanceProfileManifest(performanceProfile *performancev2.PerformanceProfile, nodePoolName string) {
	// Make PerformanceProfile names unique if a PerformanceProfile is duplicated across NodePools
	// for example, if one ConfigMap is referenced in multiple NodePools
	performanceProfile.SetName(performanceProfile.ObjectMeta.Name + "-" + hashStruct(nodePoolName))
	klog.V(2).Infof("updatePerformanceProfile: name: %s", performanceProfile.GetName())

	// Propagate NodePool name from ConfigMap down to PerformanceProfile object
	if performanceProfile.Labels == nil {
		performanceProfile.Labels = make(map[string]string)
	}
	performanceProfile.Labels[hypershiftNodePoolNameLabel] = nodePoolName
}

// Ensure PerformanceProfile has the proper label and annotations and update it in the API server
func updatePerformanceProfile(performanceProfile *performancev2.PerformanceProfile, ctx context.Context, cli client.Client, ppConfigMap *corev1.ConfigMap, nodePoolName string) error {
	updatePerformanceProfileManifest(performanceProfile, nodePoolName)
	ppEncoded, err := encodePerformanceProfile(performanceProfile)
	if err != nil {
		klog.Errorf("failed to update performance profile manifest from configMap %q: %v", ppConfigMap.Name, err)
		return err
	}

	ppConfigMap.Data[tunedConfigMapConfigKey] = string(ppEncoded)
	if err := cli.Update(ctx, ppConfigMap); err != nil {
		klog.Errorf("failed to update performance profile from configMap %q: %v", ppConfigMap.Name, err)
		return err
	}

	return nil
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
