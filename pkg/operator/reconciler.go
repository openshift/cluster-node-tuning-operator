package operator

import (
	"context"
	"os"
	"reflect"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	errors "k8s.io/apimachinery/pkg/api/errors"
	k8serros "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
)

// Reconciler reconciles a Tuned object and creates the tuned daemon set.
type Reconciler struct {
	Client    client.Client
	Scheme    *runtime.Scheme
	Namespace string
}

// SetupWithManager creates a new ClusterOperator Controller and adds it to the Manager.
// The Manager will set fields on the Controller and Start it when the Manager is Started.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// We want to initiate reconcile loop only on changes under the NTO cluster operator.
	cvoPredicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if e.Object == nil {
				klog.Error("create event has no runtime object to create")
				return false
			}

			return e.Object.GetName() == tunedv1.TunedClusterOperatorResourceName
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			return e.ObjectNew.GetName() == tunedv1.TunedClusterOperatorResourceName &&
				e.ObjectOld.GetName() == tunedv1.TunedClusterOperatorResourceName
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if e.Object == nil {
				klog.Error("delete event has no runtime object to delete")
				return false
			}

			return e.Object.GetName() == tunedv1.TunedClusterOperatorResourceName
		},
	}

	dsUpdatePredicate := predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		if !validateUpdateEvent(&e) {
			return false
		}

		dsOld := e.ObjectOld.(*appsv1.DaemonSet)
		dsNew := e.ObjectNew.(*appsv1.DaemonSet)

		return !equality.Semantic.DeepEqual(dsOld.Status, dsNew.Status) ||
			!equality.Semantic.DeepEqual(dsOld.Spec, dsNew.Spec)
	}}

	tunedPredicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if e.Object == nil {
				klog.Error("create event has no runtime object to create")
				return false
			}
			tuned := e.Object.(*tunedv1.Tuned)
			return tuned.Spec.ManagementState != ""
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			tunedOld := e.ObjectOld.(*tunedv1.Tuned)
			tunedNew := e.ObjectNew.(*tunedv1.Tuned)

			return !reflect.DeepEqual(tunedOld.Spec.ManagementState, tunedNew.Spec.ManagementState)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			tuned := e.Object.(*tunedv1.Tuned)
			return !e.DeleteStateUnknown && tuned.Spec.ManagementState != ""
		},
	}

	profilePredicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if e.Object == nil {
				klog.Error("create event has no runtime object to create")
				return false
			}
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}
			profileOld := e.ObjectOld.(*tunedv1.Profile)
			profileNew := e.ObjectNew.(*tunedv1.Profile)
			return !reflect.DeepEqual(profileOld.Status.Conditions, profileNew.Status.Conditions)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return !e.DeleteStateUnknown
		},
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(cvoPredicates)).
		Owns(&appsv1.DaemonSet{}, builder.WithPredicates(dsUpdatePredicate)).
		Owns(&tunedv1.Tuned{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&source.Kind{Type: &tunedv1.Tuned{}},
			handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      tunedv1.TunedClusterOperatorResourceName,
						Namespace: ntoconfig.OperatorNamespace(),
					}},
				}
			}),
			builder.WithPredicates(tunedPredicates)).
		Watches(
			&source.Kind{Type: &tunedv1.Profile{}},
			handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      tunedv1.TunedClusterOperatorResourceName,
						Namespace: ntoconfig.OperatorNamespace(),
					}},
				}
			}),
			builder.WithPredicates(profilePredicates)).
		Complete(r)
	if err != nil {
		return err
	}
	return nil
}

func validateUpdateEvent(e *event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		klog.Error("update event has no old runtime object")
		return false
	}
	if e.ObjectNew == nil {
		klog.Error("update event has no new runtime object")
		return false
	}

	return true
}

// Reconcile reconciles when NTO ClusterOperator is created, updated and deleted. It also creates default Tuned CR and Tuned daemonset.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	instance := &configv1.ClusterOperator{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8serros.IsNotFound(err) {
			// Request object could not be found, it could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	tunedList := &tunedv1.TunedList{}
	if err := r.Client.List(context.TODO(), tunedList); err != nil {
		klog.Errorf("failed to list Tuned CRs for cluster operator %q status conditions: %v", err, instance.Name)
		return reconcile.Result{}, err
	}

	// First inspect the ManagementState. In case the operator ManagementState is Unmanaged or Removed, update the status and take no further actions.
	for _, tuned := range tunedList.Items {
		switch tuned.Spec.ManagementState {
		case operatorv1.Unmanaged:
			if err := r.updateClusterOperatorStatus(
				ctx,
				instance,
				configv1.OperatorAvailable,
				string(operatorv1.Unmanaged),
				"The operator configuration is set to unmanaged mode",
			); err != nil {
				klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		case operatorv1.Removed:
			if err := r.updateClusterOperatorStatus(
				ctx,
				instance,
				configv1.OperatorAvailable,
				string(operatorv1.Removed),
				"The operator is removed",
			); err != nil {
				klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
	}

	klog.V(2).Infof("reconciling ClusterOperator %q", instance.Name)
	if err := r.syncDefaultTuned(ctx, instance); err != nil {
		if err := r.updateClusterOperatorStatus(
			ctx,
			instance,
			configv1.OperatorDegraded,
			conditionFailedToSyncDefaultTuned,
			err.Error(),
		); err != nil {
			klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	if err := r.syncDaemonSet(ctx, instance); err != nil {
		if err := r.updateClusterOperatorStatus(
			ctx,
			instance,
			configv1.OperatorDegraded,
			conditionFailedToSyncTunedDaemonSet,
			err.Error(),
		); err != nil {
			klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	tunedDS := manifests.TunedDaemonSet()
	tunedDS.Namespace = r.Namespace
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(tunedDS), tunedDS); err != nil {
		if err := r.updateClusterOperatorStatus(
			ctx,
			instance,
			configv1.OperatorDegraded,
			conditionFailedToGetTunedDaemonSet,
			err.Error(),
		); err != nil {
			klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	// TODO: I unsure if the current logic is enough to catch all corner cases
	// might drop that in favor of r.syncOperatorStatus()
	if tunedDS.Status.DesiredNumberScheduled != tunedDS.Status.UpdatedNumberScheduled ||
		tunedDS.Status.DesiredNumberScheduled != tunedDS.Status.NumberReady {
		if err := r.updateClusterOperatorStatus(
			ctx,
			instance,
			configv1.OperatorProgressing,
			conditionTunedDaemonSetProgressing,
			"the number of updated ready daemon set pods does not equal to desired number",
		); err != nil {
			klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}

	if err := r.syncOperatorStatus(); err != nil {
		klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
		return reconcile.Result{}, err
	}

	/*
		if err := r.updateClusterOperatorStatus(
			ctx,
			instance,
			configv1.OperatorAvailable,
			conditionSucceededToDeployComponents,
			"the operator succeeded to deploy all components",
		); err != nil {
			klog.Errorf("failed to update the cluster operator %q status conditions", instance.Name)
			return reconcile.Result{}, err
		}
	*/
	return reconcile.Result{}, nil
}

func (r *Reconciler) syncDaemonSet(ctx context.Context, instance *configv1.ClusterOperator) error {
	dsMf := ntomf.TunedDaemonSet()
	controllerutil.SetControllerReference(instance, dsMf, r.Scheme)
	ds := &appsv1.DaemonSet{}
	key := types.NamespacedName{
		Name:      dsMf.Name,
		Namespace: r.Namespace,
	}
	err := r.Client.Get(ctx, key, ds)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncDaemonSet(): DaemonSet %s not found, creating one", dsMf.Name)
			if err := r.Client.Create(ctx, dsMf); err != nil {
				klog.Errorf("failed to create DaemonSet %s: %v", dsMf.Name, err)
				return err
			}
			// DaemonSet created successfully
			return nil
		}
		klog.Errorf("failed to get DaemonSet %s: %v", dsMf.Name, err)
		return err
	}

	operatorReleaseVersion := os.Getenv("RELEASE_VERSION")
	operandReleaseVersion := ""

	for _, e := range ds.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "RELEASE_VERSION" {
			operandReleaseVersion = e.Value
			break
		}
	}

	ds = ds.DeepCopy()
	ds.Spec = dsMf.Spec

	if operatorReleaseVersion != operandReleaseVersion {
		// Update the DaemonSet
		klog.V(2).Infof("syncDaemonSet(): operatorReleaseVersion (%s) != operandReleaseVersion (%s), updating", operatorReleaseVersion, operandReleaseVersion)
		if err := r.Client.Update(ctx, ds); err != nil {
			klog.Errorf("failed to update DaemonSet %s: %v", ds.Name, err)
			return err
		}
		// DaemonSet updated successfully
		return nil
	}
	// DaemonSet comparison is non-trivial and expensive
	klog.V(2).Infof("syncDaemonSet(): found DaemonSet %s [%s], not changing it", ds.Name, operatorReleaseVersion)
	return nil
}

func (r *Reconciler) syncDefaultTuned(ctx context.Context, instance *configv1.ClusterOperator) error {
	crMf := ntomf.TunedCustomResource()
	controllerutil.SetControllerReference(instance, crMf, r.Scheme)
	cr := &tunedv1.Tuned{}
	key := types.NamespacedName{
		Name:      crMf.Name,
		Namespace: r.Namespace,
	}
	err := r.Client.Get(ctx, key, cr)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Infof("syncDefaultTuned(): default Tuned %s not found, creating one", crMf.Name)
			if err := r.Client.Create(ctx, crMf); err != nil {
				klog.Errorf("failed to create default Tuned %s: %v", crMf.Name, err)
				return err
			}
			// Tuned resource created successfully
			return nil
		}
		klog.Errorf("failed to get default Tuned %s: %v", crMf.Name, err)
		return err
	}
	// Tuned resource found, check whether we need to update it
	if reflect.DeepEqual(crMf.Spec.Profile, cr.Spec.Profile) &&
		reflect.DeepEqual(crMf.Spec.Recommend, cr.Spec.Recommend) {
		klog.V(2).Infof("syncTunedDefault(): Tuned %s doesn't need updating", crMf.Name)
		return nil
	}
	cr = cr.DeepCopy() // never update the objects from cache
	cr.Spec.Profile = crMf.Spec.Profile
	cr.Spec.Recommend = crMf.Spec.Recommend

	klog.V(2).Infof("syncTunedDefault(): updating Tuned %s", crMf.Name)
	if err := r.Client.Update(ctx, cr); err != nil {
		klog.Errorf("failed to update default Tuned %s: %v", cr.Name, err)
		return err
	}
	return nil
}
