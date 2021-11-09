package operator

import (
	"context"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serros "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
)

const ClusterOperatorName = "node-tuning"

// Reconciler reconciles a Tuned object and creates the tuned daemon set
type Reconciler struct {
	Client    client.Client
	Scheme    *runtime.Scheme
	Namespace string
}

// SetupWithManager creates a new PerformanceProfile Controller and adds it to the Manager.
// The Manager will set fields on the Controller and Start it when the Manager is Started.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// we want to initiate reconcile loop only on changes under the NTO cluster operator
	cvoPredicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if e.Object == nil {
				klog.Error("Create event has no runtime object to create")
				return false
			}

			return e.Object.GetName() == ClusterOperatorName
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !validateUpdateEvent(&e) {
				return false
			}

			return e.ObjectNew.GetName() == ClusterOperatorName && e.ObjectOld.GetName() == ClusterOperatorName
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if e.Object == nil {
				klog.Error("Delete event has no runtime object to delete")
				return false
			}

			return e.Object.GetName() == ClusterOperatorName
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
	err := ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(cvoPredicates)).
		Owns(&appsv1.DaemonSet{}, builder.WithPredicates(dsUpdatePredicate)).
		Owns(&tunedv1.Tuned{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
	if err != nil {
		return err
	}
	return nil
}

func validateUpdateEvent(e *event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		klog.Error("Update event has no old runtime object to update")
		return false
	}
	if e.ObjectNew == nil {
		klog.Error("Update event has no new runtime object for update")
		return false
	}

	return true
}

// Reconcile reconciles once NTO ClusterOperator created, updated and deleted,
// and creates NTO default tuned and tuned daemon
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	instance := &configv1.ClusterOperator{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8serros.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	klog.Infof("Reconciling ClusterOperator %q", instance.Name)
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
		// daemon set
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}

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
	return reconcile.Result{}, nil
}

func (r *Reconciler) syncDaemonSet(ctx context.Context, instance *configv1.ClusterOperator) error {
	desired := manifests.TunedDaemonSet()
	desired.Namespace = r.Namespace

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.OwnerReferences = nil
		return controllerutil.SetControllerReference(instance, desired, r.Scheme)
	})

	if err != nil {
		klog.Errorf("failed to sync daemon set: %w", err)
		return err
	}

	klog.Infof("Succeeded to sync daemon set: operation %q", op)
	return nil
}

func (r *Reconciler) syncDefaultTuned(ctx context.Context, instance *configv1.ClusterOperator) error {
	desired := manifests.TunedCustomResource()
	desired.Namespace = r.Namespace

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		desired.OwnerReferences = nil
		return controllerutil.SetControllerReference(instance, desired, r.Scheme)
	})

	if err != nil {
		klog.Errorf("failed to sync default tuned: %w", err)
		return err
	}

	klog.Infof("Succeeded to sync default tuned: operation %q", op)
	return nil
}
