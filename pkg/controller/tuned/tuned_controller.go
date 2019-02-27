package tuned

import (
	"context"
	"fmt"
	"time"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/manifests"

	"github.com/golang/glog"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Add creates a new Tuned Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileTuned{
		client: mgr.GetClient(), scheme: mgr.GetScheme(),
		manifestFactory: manifests.NewFactory(),
		cfgv1client:     nil,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	const (
		sleepRetry = 10
		errsMax    = 5
	)

	// Create a new controller
	c, err := controller.New("tuned-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Tuned
	err = c.Watch(&source.Kind{Type: &tunedv1.Tuned{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource DaemonSet and requeue the owner Tuned
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &tunedv1.Tuned{},
	})
	if err != nil {
		return err
	}

	// A retry loop for errors like "caches not synchronized"
	errs := 0
	for {
		err = createCustomResource(mgr)
		if err != nil {
			glog.Errorf("createCustomResource(): %v", err)
			if errs++; errs >= errsMax {
				return err
			}
			time.Sleep(time.Second * sleepRetry)
			continue
		}
		break
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileTuned{}

// ReconcileTuned reconciles a Tuned object
type ReconcileTuned struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client          client.Client
	scheme          *runtime.Scheme
	manifestFactory *manifests.Factory
	cfgv1client     *configv1client.ConfigV1Client
}

func (r *ReconcileTuned) syncServiceAccount(tuned *tunedv1.Tuned) error {
	glog.V(1).Infof("syncServiceAccount()")
	saManifest, err := r.manifestFactory.TunedServiceAccount()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ServiceAccount: %v", err)
	}

	sa := &corev1.ServiceAccount{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: saManifest.Namespace, Name: saManifest.Name}, sa)
	saManifest.SetOwnerReferences(addOwnerReference(&sa.ObjectMeta, tuned))
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), saManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ServiceAccount: %v", err)
			}
			glog.V(2).Infof("Created ServiceAccount for %s/%s", saManifest.Namespace, saManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ServiceAccount: %v\n", err)
		}
	} else {
		glog.V(2).Infof("Tuned ServiceAccount already exists, updating")
		err = r.client.Update(context.TODO(), saManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ServiceAccount: %v", err)
		}
	}

	return nil
}

func (r *ReconcileTuned) syncClusterRole(tuned *tunedv1.Tuned) error {
	glog.V(1).Infof("syncClusterRole()")
	crManifest, err := r.manifestFactory.TunedClusterRole()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ClusterRole: %v", err)
	}

	cr := &rbacv1.ClusterRole{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: "", Name: crManifest.Name}, cr)
	crManifest.SetOwnerReferences(addOwnerReference(&cr.ObjectMeta, tuned))
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), crManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ClusterRole: %v", err)
			}
			glog.V(2).Infof("Created ClusterRole for %s", crManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ClusterRole: %v\n", err)
		}
	} else {
		glog.V(2).Infof("Tuned ClusterRole already exists, updating")
		err = r.client.Update(context.TODO(), crManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ClusterRole: %v", err)
		}
	}

	return nil
}

func (r *ReconcileTuned) syncClusterRoleBinding(tuned *tunedv1.Tuned) error {
	glog.V(1).Infof("syncClusterRoleBinding()")
	crbManifest, err := r.manifestFactory.TunedClusterRoleBinding()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ClusterRoleBinding: %v", err)
	}

	crb := &rbacv1.ClusterRoleBinding{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: "", Name: crbManifest.Name}, crb)
	crbManifest.SetOwnerReferences(addOwnerReference(&crb.ObjectMeta, tuned))
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), crbManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ClusterRoleBinding: %v", err)
			}
			glog.V(2).Infof("Created ClusterRoleBinding for %s", crbManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ClusterRoleBinding: %v", err)
		}
	} else {
		glog.V(2).Infof("Tuned ClusterRoleBinding already exists, updating")
		err = r.client.Update(context.TODO(), crbManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ClusterRoleBinding: %v", err)
		}
	}

	return nil
}

func (r *ReconcileTuned) syncClusterConfigMap(f func(tuned []tunedv1.Tuned) (*corev1.ConfigMap, error), tuned *tunedv1.Tuned) error {
	glog.V(1).Infof("syncClusterConfigMap()")
	tunedList := &tunedv1.TunedList{}
	listOps := &client.ListOptions{Namespace: tuned.Namespace}
	err := r.client.List(context.TODO(), listOps, tunedList)
	if err != nil {
		return fmt.Errorf("Couldn't list Tuned: %v", err)
	}

	cmManifest, err := f(tunedList.Items)
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ConfigMap: %v", err)
	}

	cm := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: cmManifest.Namespace, Name: cmManifest.Name}, cm)
	cmManifest.SetOwnerReferences(addOwnerReference(&cm.ObjectMeta, tuned))
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), cmManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ConfigMap: %v", err)
			}
			glog.V(2).Infof("Created ConfigMap for %s/%s", cmManifest.Namespace, cmManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ConfigMap: %vn", err)
		}
	} else {
		glog.V(2).Infof("Tuned ConfigMap %s already exists, updating", cmManifest.Name)
		err = r.client.Update(context.TODO(), cmManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ConfigMap: %v", err)
		}
	}

	return nil
}

func (r *ReconcileTuned) syncDaemonSet(tuned *tunedv1.Tuned) error {
	glog.V(1).Infof("syncDaemonSet()")
	dsManifest, err := r.manifestFactory.TunedDaemonSet()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned DaemonSet: %v", err)
	}

	daemonset := &appsv1.DaemonSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: dsManifest.Namespace, Name: dsManifest.Name}, daemonset)
	dsManifest.SetOwnerReferences(addOwnerReference(&daemonset.ObjectMeta, tuned))
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), dsManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned DaemonSet: %v", err)
			}
			glog.V(2).Infof("Created DaemonSet for %s/%s", dsManifest.Namespace, dsManifest.Name)
		} else {
			return fmt.Errorf("Failed to get DaemonSet: %v", err)
		}
	} else {
		glog.V(2).Infof("Tuned DaemonSet already exists, updating")
		err = r.client.Update(context.TODO(), dsManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned DaemonSet: %v", err)
		}
	}

	return nil
}

func createCustomResource(mgr manager.Manager) error {
	client := mgr.GetClient()
	manifestFactory := manifests.NewFactory()

	// Get the default configuration (CR) for Tuned
	crManifest, err := manifestFactory.TunedCustomResource()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned CustomResource: %v", err)
	}

	err = client.Create(context.TODO(), crManifest)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("Couldn't create tuned CustomResource: %v", err)
		}
	}

	return nil
}

func addOwnerReference(meta *metav1.ObjectMeta, tuned *tunedv1.Tuned) []metav1.OwnerReference {
	var isController bool
	if tuned.Name == "default" {
		isController = true
	}

	if meta.OwnerReferences == nil {
		meta.OwnerReferences = []metav1.OwnerReference{}
	} else {
		for _, owner := range meta.OwnerReferences {
			if owner.UID == tuned.UID {
				// Owner reference already set
				return meta.OwnerReferences
			}
		}
	}

	ownerReference := metav1.OwnerReference{
		APIVersion: tunedv1.SchemeGroupVersion.String(),
		Kind:       "Tuned",
		Name:       tuned.Name,
		UID:        tuned.UID,
		Controller: &isController,
	}

	return append(meta.OwnerReferences, ownerReference)
}

// Reconcile reads that state of the cluster for a Tuned object and makes changes based on the state read
// and what is in the Tuned.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileTuned) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	var requeue bool
	glog.V(1).Infof("Reconciling Tuned %s/%s", request.Namespace, request.Name)

	resyncPeriodDuration := ntoconfig.ResyncPeriod()
	reconcilePeriod := time.Duration(resyncPeriodDuration) * time.Second
	reconcileResult := reconcile.Result{RequeueAfter: reconcilePeriod}

	// Fetch the Tuned instance
	tunedInstance := &tunedv1.Tuned{}
	err := r.client.Get(context.TODO(), request.NamespacedName, tunedInstance)
	if err != nil {
		glog.Errorf("Couldn't get tunedInstance(): %v", err)
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue

			return reconcile.Result{Requeue: false}, nil
		}
		// Error reading the object - requeue the request.
		return reconcileResult, err
	}

	err = r.syncServiceAccount(tunedInstance)
	if err != nil {
		glog.Errorf("Couldn't syncServiceAccount(): %v", err)
		return reconcileResult, err
	}

	err = r.syncClusterRole(tunedInstance)
	if err != nil {
		glog.Errorf("Couldn't syncClusterRole(): %v", err)
		return reconcileResult, err
	}

	err = r.syncClusterRoleBinding(tunedInstance)
	if err != nil {
		glog.Errorf("Couldn't syncClusterRoleBinding(): %v", err)
		return reconcileResult, err
	}

	err = r.syncClusterConfigMap(r.manifestFactory.TunedConfigMapProfiles, tunedInstance)
	if err != nil {
		glog.Errorf("Couldn't syncClusterConfigMap(): %v", err)
		return reconcileResult, err
	}

	err = r.syncClusterConfigMap(r.manifestFactory.TunedConfigMapRecommend, tunedInstance)
	if err != nil {
		glog.Errorf("Couldn't syncClusterConfigMap(): %v", err)
		return reconcileResult, err
	}

	err = r.syncDaemonSet(tunedInstance)
	if err != nil {
		glog.Errorf("Couldn't syncDaemonSet(): %v", err)
		return reconcileResult, err
	}

	requeue, err = r.syncOperatorStatus()
	if err != nil {
		glog.Errorf("Couldn't syncOperatorStatus(): %v", err)
		return reconcileResult, err
	} else if requeue {
		glog.Errorf("Reconcile requeue due to syncOperatorStatus()")
		return reconcile.Result{Requeue: true}, nil
	}

	return reconcileResult, nil
}
