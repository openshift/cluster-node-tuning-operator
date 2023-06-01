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
	"fmt"

	ntconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const controllerGeneratedMachineConfig = "hypershift.openshift.io/performanceprofile-config"

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
