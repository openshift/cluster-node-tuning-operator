package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"

	nodev1 "k8s.io/api/node/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

func mergeMaps(src map[string]string, dst map[string]string) {
	for k, v := range src {
		// NOTE: it will override destination values
		dst[k] = v
	}
}

// TODO: we should merge all create, get and delete methods

func (r *PerformanceProfileReconciler) getCurrentMachineConfigByMCP(ctx context.Context, mcp *mcov1.MachineConfigPool) (*mcov1.MachineConfig, error) {
	if mcp == nil {
		return nil, fmt.Errorf("nil MachineConfigPool object")
	}

	currentMCName := mcp.Status.Configuration.Name
	if currentMCName == "" { // should never happen
		return nil, fmt.Errorf("MCP %q missing configuration name", mcp.Name)
	}

	klog.Infof("checking current MachineConfig %q", currentMCName)

	return r.getMachineConfig(ctx, currentMCName)
}

func (r *PerformanceProfileReconciler) getMachineConfig(ctx context.Context, name string) (*mcov1.MachineConfig, error) {
	mc := &mcov1.MachineConfig{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	if err := r.Get(ctx, key, mc); err != nil {
		return nil, err
	}
	return mc, nil
}

func (r *PerformanceProfileReconciler) getMutatedMachineConfig(ctx context.Context, mc *mcov1.MachineConfig) (*mcov1.MachineConfig, error) {
	existing, err := r.getMachineConfig(ctx, mc.Name)
	if errors.IsNotFound(err) {
		return mc, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(mc.Annotations, mutated.Annotations)
	mergeMaps(mc.Labels, mutated.Labels)
	mutated.Spec = mc.Spec

	// we do not need to update if it no change between mutated and existing object
	if reflect.DeepEqual(existing.Spec, mutated.Spec) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func (r *PerformanceProfileReconciler) getClusterOperator() (*apiconfigv1.ClusterOperator, error) {
	co := &apiconfigv1.ClusterOperator{}
	key := types.NamespacedName{
		Name:      "node-tuning",
		Namespace: metav1.NamespaceNone,
	}
	if err := r.Get(context.TODO(), key, co); err != nil {
		return nil, err
	}
	return co, nil
}

func (r *PerformanceProfileReconciler) createOrUpdateMachineConfig(mc *mcov1.MachineConfig) error {
	_, err := r.getMachineConfig(context.TODO(), mc.Name)
	if errors.IsNotFound(err) {
		klog.Infof("Create machine-config %q", mc.Name)
		if err := r.Create(context.TODO(), mc); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Update machine-config %q", mc.Name)
	return r.Update(context.TODO(), mc)
}

func (r *PerformanceProfileReconciler) deleteMachineConfig(name string) error {
	mc, err := r.getMachineConfig(context.TODO(), name)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.Delete(context.TODO(), mc)
}

func (r *PerformanceProfileReconciler) getKubeletConfig(name string) (*mcov1.KubeletConfig, error) {
	kc := &mcov1.KubeletConfig{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	if err := r.Get(context.TODO(), key, kc); err != nil {
		return nil, err
	}
	return kc, nil
}

func (r *PerformanceProfileReconciler) getMutatedKubeletConfig(kc *mcov1.KubeletConfig) (*mcov1.KubeletConfig, error) {
	existing, err := r.getKubeletConfig(kc.Name)
	if errors.IsNotFound(err) {
		return kc, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(kc.Annotations, mutated.Annotations)
	mergeMaps(kc.Labels, mutated.Labels)
	mutated.Spec = kc.Spec

	existingKubeletConfig := &kubeletconfigv1beta1.KubeletConfiguration{}
	err = json.Unmarshal(existing.Spec.KubeletConfig.Raw, existingKubeletConfig)
	if err != nil {
		return nil, err
	}

	mutatedKubeletConfig := &kubeletconfigv1beta1.KubeletConfiguration{}
	err = json.Unmarshal(mutated.Spec.KubeletConfig.Raw, mutatedKubeletConfig)
	if err != nil {
		return nil, err
	}

	// we do not need to update if it no change between mutated and existing object
	if apiequality.Semantic.DeepEqual(existingKubeletConfig, mutatedKubeletConfig) &&
		apiequality.Semantic.DeepEqual(existing.Spec.MachineConfigPoolSelector, mutated.Spec.MachineConfigPoolSelector) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func (r *PerformanceProfileReconciler) createOrUpdateKubeletConfig(kc *mcov1.KubeletConfig) error {
	_, err := r.getKubeletConfig(kc.Name)
	if errors.IsNotFound(err) {
		klog.Infof("Create kubelet-config %q", kc.Name)
		if err := r.Create(context.TODO(), kc); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Update kubelet-config %q", kc.Name)
	return r.Update(context.TODO(), kc)
}

func (r *PerformanceProfileReconciler) deleteKubeletConfig(name string) error {
	kc, err := r.getKubeletConfig(name)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.Delete(context.TODO(), kc)
}

func (r *PerformanceProfileReconciler) getTuned(name string, namespace string) (*tunedv1.Tuned, error) {
	tuned := &tunedv1.Tuned{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	if err := r.Get(context.TODO(), key, tuned); err != nil {
		return nil, err
	}
	return tuned, nil
}

func (r *PerformanceProfileReconciler) getMutatedTuned(tuned *tunedv1.Tuned) (*tunedv1.Tuned, error) {
	existing, err := r.getTuned(tuned.Name, tuned.Namespace)
	if errors.IsNotFound(err) {
		return tuned, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(tuned.Annotations, mutated.Annotations)
	mergeMaps(tuned.Labels, mutated.Labels)
	mutated.Spec = tuned.Spec

	// we do not need to update if it no change between mutated and existing object
	if apiequality.Semantic.DeepEqual(existing.Spec, mutated.Spec) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func (r *PerformanceProfileReconciler) createOrUpdateTuned(tuned *tunedv1.Tuned, profileName string) error {
	if err := r.removeOutdatedTuned(tuned, profileName); err != nil {
		return err
	}

	_, err := r.getTuned(tuned.Name, tuned.Namespace)
	if errors.IsNotFound(err) {
		klog.Infof("Create tuned %q under the namespace %q", tuned.Name, tuned.Namespace)
		if err := r.Create(context.TODO(), tuned); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Update tuned %q under the namespace %q", tuned.Name, tuned.Namespace)
	return r.Update(context.TODO(), tuned)
}

func (r *PerformanceProfileReconciler) removeOutdatedTuned(tuned *tunedv1.Tuned, profileName string) error {
	tunedList := &tunedv1.TunedList{}
	if err := r.List(context.TODO(), tunedList); err != nil {
		klog.Errorf("Unable to list tuned objects for outdated removal procedure: %v", err)
		return err
	}

	for t := range tunedList.Items {
		tunedItem := tunedList.Items[t]
		ownerReferences := tunedItem.ObjectMeta.OwnerReferences
		for o := range ownerReferences {
			if ownerReferences[o].Name == profileName && tunedItem.Name != tuned.Name {
				if err := r.deleteTuned(tunedItem.Name, tunedItem.Namespace); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *PerformanceProfileReconciler) deleteTuned(name string, namespace string) error {
	tuned, err := r.getTuned(name, namespace)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.Delete(context.TODO(), tuned)
}

func (r *PerformanceProfileReconciler) getRuntimeClass(name string) (*nodev1.RuntimeClass, error) {
	runtimeClass := &nodev1.RuntimeClass{}
	key := types.NamespacedName{
		Name: name,
	}
	if err := r.Get(context.TODO(), key, runtimeClass); err != nil {
		return nil, err
	}
	return runtimeClass, nil
}

func (r *PerformanceProfileReconciler) getMutatedRuntimeClass(runtimeClass *nodev1.RuntimeClass) (*nodev1.RuntimeClass, error) {
	existing, err := r.getRuntimeClass(runtimeClass.Name)
	if errors.IsNotFound(err) {
		return runtimeClass, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(runtimeClass.Annotations, mutated.Annotations)
	mergeMaps(runtimeClass.Labels, mutated.Labels)
	mutated.Handler = runtimeClass.Handler
	mutated.Scheduling = runtimeClass.Scheduling

	// we do not need to update if it no change between mutated and existing object
	if apiequality.Semantic.DeepEqual(existing.Handler, mutated.Handler) &&
		apiequality.Semantic.DeepEqual(existing.Scheduling, mutated.Scheduling) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func (r *PerformanceProfileReconciler) createOrUpdateRuntimeClass(runtimeClass *nodev1.RuntimeClass) error {
	_, err := r.getRuntimeClass(runtimeClass.Name)
	if errors.IsNotFound(err) {
		klog.Infof("Create runtime class %q", runtimeClass.Name)
		if err := r.Create(context.TODO(), runtimeClass); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.Infof("Update runtime class %q", runtimeClass.Name)
	return r.Update(context.TODO(), runtimeClass)
}

func (r *PerformanceProfileReconciler) deleteRuntimeClass(name string) error {
	runtimeClass, err := r.getRuntimeClass(name)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.Delete(context.TODO(), runtimeClass)
}
