package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/nodeplugin"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/nodeplugin/apply"
)

func mergeMaps(src map[string]string, dst map[string]string) {
	for k, v := range src {
		// NOTE: it will override destination values
		dst[k] = v
	}
}

// TODO: we should merge all create, get and delete methods

func (r *PerformanceProfileReconciler) getMachineConfig(name string) (*mcov1.MachineConfig, error) {
	mc := &mcov1.MachineConfig{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	if err := r.Get(context.TODO(), key, mc); err != nil {
		return nil, err
	}
	return mc, nil
}

func (r *PerformanceProfileReconciler) getMutatedMachineConfig(mc *mcov1.MachineConfig) (*mcov1.MachineConfig, error) {
	existing, err := r.getMachineConfig(mc.Name)
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

func (r *PerformanceProfileReconciler) createOrUpdateMachineConfig(mc *mcov1.MachineConfig) error {
	_, err := r.getMachineConfig(mc.Name)
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
	mc, err := r.getMachineConfig(name)
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

func (r *PerformanceProfileReconciler) applyNodePluginComponents(ctx context.Context, profile *performancev2.PerformanceProfile, nodePluginComponents *nodeplugin.Components) (bool, error) {
	var mutated bool
	uns, err := nodePluginComponents.ToUnstructured()
	if err != nil {
		return mutated, err
	}

	for _, updated := range uns {
		if err := controllerutil.SetControllerReference(profile, updated, r.Scheme); err != nil {
			return mutated, err
		}

		// get the existing object
		gvk := updated.GetObjectKind().GroupVersionKind()
		key := fmt.Sprintf("(%s) %s/%s", gvk, updated.GetNamespace(), updated.GetName())
		current := &unstructured.Unstructured{}
		current.SetGroupVersionKind(gvk)
		if err := r.Get(ctx, client.ObjectKeyFromObject(updated), current); err != nil {
			if errors.IsNotFound(err) {
				klog.Infof("create %q", key)
				if err = r.Create(ctx, updated); err != nil {
					return mutated, fmt.Errorf("failed to Create %q; %w", key, err)
				}
				mutated = true
			} else {
				return mutated, fmt.Errorf("failed to Get %q; %w", key, err)
			}
		}

		if err := apply.MergeObjectForUpdate(current, updated); err != nil {
			return mutated, err
		}

		if !equality.Semantic.DeepDerivative(updated, current) {
			if err := r.Update(ctx, updated); err != nil {
				return mutated, fmt.Errorf("failed to Update %q; %w", key, err)
			} else {
				klog.Infof("Update %q successfully", key)
				mutated = true
			}
		}
	}
	return mutated, nil
}

func (r *PerformanceProfileReconciler) deleteNodePluginComponents(ctx context.Context, nodePluginComponents *nodeplugin.Components) (bool, error) {
	objs, err := nodePluginComponents.ToUnstructured()
	if err != nil {
		return false, err
	}
	var mutated bool
	for _, obj := range objs {
		gvk := obj.GetObjectKind().GroupVersionKind()
		key := fmt.Sprintf("(%s) %s/%s", gvk, obj.GetNamespace(), obj.GetName())
		err := r.Delete(ctx, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return false, fmt.Errorf("failed to Delete %q; %w", key, err)
		}
		klog.Infof("Delete %q successfully", key)
		mutated = true
	}
	return mutated, nil
}
