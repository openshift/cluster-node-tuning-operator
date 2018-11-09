package tuned

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	tunedv1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1alpha1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/manifests"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	resyncPeriodDefault int64 = 600
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
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("tuned-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Tuned
	err = c.Watch(&source.Kind{Type: &tunedv1alpha1.Tuned{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO: Get secondary watch working
	// Watch for changes to secondary resource DaemonSet and requeue the owner Tuned
	//err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
	//	IsController: true,
	//	OwnerType:    &tunedv1alpha1.Tuned{},
	//})
	//if err != nil {
	//	return err
	//}

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
}

func syncNamespace(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncNamespace()")
	ns := &corev1.Namespace{}

	nsManifest, err := r.manifestFactory.TunedNamespace()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned Namespace: %v", err)
	}
	log.Printf("Built Namespace manifest for %s\n", nsManifest.Name)

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: nsManifest.Name, Namespace: ""}, ns)
	if err != nil && errors.IsNotFound(err) {

		controllerutil.SetControllerReference(tuned, nsManifest, r.scheme)
		err = r.client.Create(context.TODO(), nsManifest)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned Namespace: %v", err)
		}
		log.Printf("Created Namespace for %s\n", nsManifest.Name)
	} else if err != nil {
		log.Printf("Failed to get Namespace: %v\n", err)
		return err
	}

	return nil
}

func syncServiceAccount(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncServiceAccount()")
	saManifest, err := r.manifestFactory.TunedServiceAccount()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ServiceAccount: %v", err)
	}
	log.Printf("Built ServiceAccount manifest for %s/%s\n", saManifest.Namespace, saManifest.Name)

	sa := &corev1.ServiceAccount{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: saManifest.Name, Namespace: saManifest.Namespace}, sa)
	if err != nil && errors.IsNotFound(err) {
		controllerutil.SetControllerReference(tuned, saManifest, r.scheme)
		err = r.client.Create(context.TODO(), saManifest)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned ServiceAccount: %v", err)
		}
		log.Printf("Created ServiceAccount for %s/%s\n", saManifest.Namespace, saManifest.Name)
	} else if err != nil {
		log.Printf("Failed to get ServiceAccount: %v\n", err)
		return err
	} else {
		log.Printf("Tuned ServiceAccount already exists, updating")
		err = r.client.Update(context.TODO(), saManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ServiceAccount: %v", err)
		}
	}

	return nil
}

func syncClusterRole(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterRole()")
	crManifest, err := r.manifestFactory.TunedClusterRole()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ClusterRole: %v", err)
	}
	log.Printf("Built ClusterRole manifest for %s\n", crManifest.Name)

	cr := &rbacv1.ClusterRole{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: crManifest.Name, Namespace: ""}, cr)
	if err != nil && errors.IsNotFound(err) {
		controllerutil.SetControllerReference(tuned, crManifest, r.scheme)
		err = r.client.Create(context.TODO(), crManifest)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned ClusterRole: %v", err)
		}
		log.Printf("Created ClusterRole for %s\n", crManifest.Name)
	} else if err != nil {
		log.Printf("Failed to get ClusterRole: %v\n", err)
		return err
	} else {
		log.Printf("Tuned ClusterRole already exists, updating")
		err = r.client.Update(context.TODO(), crManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ClusterRole: %v", err)
		}
	}

	return nil
}

func syncClusterRoleBinding(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterRoleBinding()")
	crbManifest, err := r.manifestFactory.TunedClusterRoleBinding()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ClusterRoleBinding: %v", err)
	}
	log.Printf("Built ClusteRoleBinding manifest for %s\n", crbManifest.Name)

	crb := &rbacv1.ClusterRoleBinding{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: crbManifest.Name, Namespace: ""}, crb)
	if err != nil && errors.IsNotFound(err) {

		controllerutil.SetControllerReference(tuned, crbManifest, r.scheme)
		err = r.client.Create(context.TODO(), crbManifest)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned ClusterRoleBinding: %v", err)
		}
		log.Printf("Created ClusterRoleBinding for %s\n", crbManifest.Name)
	} else if err != nil {
		log.Printf("Failed to get ClusterRoleBinding: %v\n", err)
		return err
	} else {
		log.Printf("Tuned ClusterRoleBinding already exists, updating")
		err = r.client.Update(context.TODO(), crbManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ClusterRoleBinding: %v", err)
		}
	}

	return nil
}

func syncClusterConfigMap(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterConfigMapProfiles()")
	cmProfiles, err := r.manifestFactory.TunedConfigMapProfiles(tuned)
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ConfigMapProfiles: %v", err)
	}
	log.Printf("Built ConfigMapProfiles manifest for %s/%s\n", cmProfiles.Namespace, cmProfiles.Name)

	cm := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cmProfiles.Name, Namespace: cmProfiles.Namespace}, cm)
	if err != nil && errors.IsNotFound(err) {
		controllerutil.SetControllerReference(tuned, cmProfiles, r.scheme)
		err = r.client.Create(context.TODO(), cmProfiles)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned ConfigMapProfiles: %v", err)
		}
		log.Printf("Created ConfigMapProfiles for %s/%s\n", cmProfiles.Namespace, cmProfiles.Name)
	} else if err != nil {
		log.Printf("Failed to get ConfigMapProfiles: %v\n", err)
		return err
	} else {
		log.Printf("Tuned ConfigMapProfiles already exists, updating")
		err = r.client.Update(context.TODO(), cmProfiles)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ConfigMapProfiles: %v", err)
		}
	}

	return nil
}

func syncClusterConfigMapProfiles(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterConfigMapProfiles()")
	cmProfiles, err := r.manifestFactory.TunedConfigMapProfiles(tuned)
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ConfigMapProfiles: %v", err)
	}
	log.Printf("Built ConfigMapProfiles manifest for %s/%s\n", cmProfiles.Namespace, cmProfiles.Name)

	cm := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cmProfiles.Name, Namespace: cmProfiles.Namespace}, cm)
	if err != nil && errors.IsNotFound(err) {
		controllerutil.SetControllerReference(tuned, cmProfiles, r.scheme)
		err = r.client.Create(context.TODO(), cmProfiles)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned ConfigMapProfiles: %v", err)
		}
		log.Printf("Created ConfigMapProfiles for %s/%s\n", cmProfiles.Namespace, cmProfiles.Name)
	} else if err != nil {
		log.Printf("Failed to get ConfigMapProfiles: %v\n", err)
		return err
	} else {
		log.Printf("Tuned ConfigMapProfiles already exists, updating")
		err = r.client.Update(context.TODO(), cmProfiles)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ConfigMapProfiles: %v", err)
		}
	}

	return nil
}

func syncClusterConfigMapRecommend(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterConfigMapRecommend()")
	cmRecommend, err := r.manifestFactory.TunedConfigMapRecommend(tuned)
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ConfigMapRecommend: %v", err)
	}
	log.Printf("Built ConfigMapRecommend manifest for %s/%s\n", cmRecommend.Namespace, cmRecommend.Name)

	cm := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cmRecommend.Name, Namespace: cmRecommend.Namespace}, cm)
	if err != nil && errors.IsNotFound(err) {
		controllerutil.SetControllerReference(tuned, cmRecommend, r.scheme)
		err = r.client.Create(context.TODO(), cmRecommend)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned ConfigMapRecommend: %v", err)
		}
		log.Printf("Created ConfigMapRecommend for %s/%s\n", cmRecommend.Namespace, cmRecommend.Name)
	} else if err != nil {
		log.Printf("Failed to get ConfigMapRecommend: %v\n", err)
		return err
	} else {
		log.Printf("Tuned ConfigMapRecommend already exists, updating")
		err = r.client.Update(context.TODO(), cmRecommend)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ConfigMapRecommend: %v", err)
		}
	}

	return nil
}

func syncDaemonSet(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	var (
		dsManifest *appsv1.DaemonSet
		err        error
	)
	log.Printf("syncDaemonSet()")
	dsManifest, err = r.manifestFactory.TunedDaemonSet()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned DaemonSet: %v", err)
	}
	log.Printf("Built DaemonSet manifest for %s/%s\n", dsManifest.Namespace, dsManifest.Name)

	daemonset := &appsv1.DaemonSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: dsManifest.Name, Namespace: dsManifest.Namespace}, daemonset)
	if err != nil && errors.IsNotFound(err) {
		controllerutil.SetControllerReference(tuned, dsManifest, r.scheme)
		err = r.client.Create(context.TODO(), dsManifest)
		if err != nil {
			return fmt.Errorf("Couldn't create tuned DaemonSet: %v", err)
		}
		log.Printf("Created DaemonSet for %s/%s\n", dsManifest.Namespace, dsManifest.Name)
	} else if err != nil {
		log.Printf("Failed to get DaemonSet: %v\n", err)
		return err
	} else {
		log.Printf("Tuned DaemonSet already exists, updating")
		err = r.client.Update(context.TODO(), dsManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned DaemonSet: %v", err)
		}
	}

	return nil
}

// Reconcile reads that state of the cluster for a Tuned object and makes changes based on the state read
// and what is in the Tuned.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileTuned) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	resyncPeriodDuration := resyncPeriodDefault

	log.Printf("Reconciling Tuned %s/%s\n", request.Namespace, request.Name)

	if os.Getenv("RESYNC_PERIOD") != "" {
		var err error
		resyncPeriodDuration, err = strconv.ParseInt(os.Getenv("RESYNC_PERIOD"), 10, 64)
		if err != nil {
			log.Printf("Cannot parse RESYNC_PERIOD (%s), using %d", os.Getenv("RESYNC_PERIOD"), resyncPeriodDefault)
			resyncPeriodDuration = resyncPeriodDefault
		}
	}
	reconcilePeriod := time.Duration(resyncPeriodDuration) * time.Second
	reconcileResult := reconcile.Result{RequeueAfter: reconcilePeriod}

	// Fetch the Tuned instance
	tunedInstance := &tunedv1alpha1.Tuned{}
	err := r.client.Get(context.TODO(), request.NamespacedName, tunedInstance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcileResult, nil
		}
		// Error reading the object - requeue the request.
		return reconcileResult, err
	}

	err = syncNamespace(r, tunedInstance)
	if err != nil {
		return reconcileResult, err
	}

	err = syncServiceAccount(r, tunedInstance)
	if err != nil {
		return reconcileResult, err
	}

	err = syncClusterRole(r, tunedInstance)
	if err != nil {
		return reconcileResult, err
	}

	err = syncClusterRoleBinding(r, tunedInstance)
	if err != nil {
		return reconcileResult, err
	}

	err = syncClusterConfigMapProfiles(r, tunedInstance)
	if err != nil {
		return reconcileResult, err
	}

	err = syncClusterConfigMapRecommend(r, tunedInstance)
	if err != nil {
		return reconcileResult, err
	}

	err = syncDaemonSet(r, tunedInstance)
	if err != nil {
		return reconcileResult, err
	}

	return reconcileResult, nil
}
