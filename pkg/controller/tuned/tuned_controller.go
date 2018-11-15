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

	// Watch for changes to secondary resource DaemonSet and requeue the owner Tuned
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &tunedv1alpha1.Tuned{},
	})
	if err != nil {
		return err
	}

	createCustomResource(mgr)
	if err != nil {
		log.Printf("createCustomResource(): %v\n", err)
		return err
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
}

func syncServiceAccount(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncServiceAccount()")
	saManifest, err := r.manifestFactory.TunedServiceAccount()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ServiceAccount: %v", err)
	}
	log.Printf("Built ServiceAccount manifest for %s/%s\n", saManifest.Namespace, saManifest.Name)

	sa := &corev1.ServiceAccount{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: saManifest.Namespace, Name: saManifest.Name}, sa)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), saManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ServiceAccount: %v", err)
			}
			log.Printf("Created ServiceAccount for %s/%s\n", saManifest.Namespace, saManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ServiceAccount: %v\n", err)
		}
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
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: "", Name: crManifest.Name}, cr)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), crManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ClusterRole: %v", err)
			}
			log.Printf("Created ClusterRole for %s\n", crManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ClusterRole: %v\n", err)
		}
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
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: "", Name: crbManifest.Name}, crb)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), crbManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ClusterRoleBinding: %v", err)
			}
			log.Printf("Created ClusterRoleBinding for %s\n", crbManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ClusterRoleBinding: %v\n", err)
		}
	} else {
		log.Printf("Tuned ClusterRoleBinding already exists, updating")
		err = r.client.Update(context.TODO(), crbManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ClusterRoleBinding: %v", err)
		}
	}

	return nil
}

func syncClusterConfigMap(f func(tuned *tunedv1alpha1.Tuned) (*corev1.ConfigMap, error), r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterConfigMap()")
	cmManifest, err := f(tuned)
	if err != nil {
		return fmt.Errorf("Couldn't build tuned ConfigMap: %v", err)
	}
	log.Printf("Built ConfigMap manifest for %s/%s\n", cmManifest.Namespace, cmManifest.Name)

	cm := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: cmManifest.Namespace, Name: cmManifest.Name}, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), cmManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned ConfigMap: %v", err)
			}
			log.Printf("Created ConfigMap for %s/%s\n", cmManifest.Namespace, cmManifest.Name)
		} else {
			return fmt.Errorf("Failed to get ConfigMap: %vn", err)
		}
	} else {
		log.Printf("Tuned ConfigMap %s already exists, updating", cmManifest.Name)
		err = r.client.Update(context.TODO(), cmManifest)
		if err != nil {
			return fmt.Errorf("Couldn't update tuned ConfigMap: %v", err)
		}
	}

	return nil
}

func syncDaemonSet(r *ReconcileTuned, tuned *tunedv1alpha1.Tuned) error {
	log.Printf("syncDaemonSet()")
	dsManifest, err := r.manifestFactory.TunedDaemonSet()
	if err != nil {
		return fmt.Errorf("Couldn't build tuned DaemonSet: %v", err)
	}
	log.Printf("Built DaemonSet manifest for %s/%s\n", dsManifest.Namespace, dsManifest.Name)

	daemonset := &appsv1.DaemonSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: dsManifest.Namespace, Name: dsManifest.Name}, daemonset)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(context.TODO(), dsManifest)
			if err != nil {
				return fmt.Errorf("Couldn't create tuned DaemonSet: %v", err)
			}
			log.Printf("Created DaemonSet for %s/%s\n", dsManifest.Namespace, dsManifest.Name)
		} else {
			return fmt.Errorf("Failed to get DaemonSet: %v\n", err)
		}
	} else {
		log.Printf("Tuned DaemonSet already exists, updating")
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
	log.Printf("Built CustomResource manifest for %s/%s\n", crManifest.Namespace, crManifest.Name)

	err = client.Create(context.TODO(), crManifest)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			log.Printf("Couldn't create tuned CustomResource: %v", err)
			return fmt.Errorf("Couldn't create tuned CustomResource: %v", err)
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
		log.Printf("Couldn't get tunedInstance(): %v\n", err)
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcileResult, nil
		}
		// Error reading the object - requeue the request.
		return reconcileResult, err
	}

	err = syncServiceAccount(r, tunedInstance)
	if err != nil {
		log.Printf("Couldn't syncServiceAccount(): %v\n", err)
		return reconcileResult, err
	}

	err = syncClusterRole(r, tunedInstance)
	if err != nil {
		log.Printf("Couldn't syncClusterRole(): %v\n", err)
		return reconcileResult, err
	}

	err = syncClusterRoleBinding(r, tunedInstance)
	if err != nil {
		log.Printf("Couldn't syncClusterRoleBinding(): %v\n", err)
		return reconcileResult, err
	}

	err = syncClusterConfigMap(r.manifestFactory.TunedConfigMapProfiles, r, tunedInstance)
	if err != nil {
		log.Printf("Couldn't syncClusterConfigMap(): %v\n", err)
		return reconcileResult, err
	}

	err = syncClusterConfigMap(r.manifestFactory.TunedConfigMapRecommend, r, tunedInstance)
	if err != nil {
		log.Printf("Couldn't syncClusterConfigMap(): %v\n", err)
		return reconcileResult, err
	}

	err = syncDaemonSet(r, tunedInstance)
	if err != nil {
		log.Printf("Couldn't syncDaemonSet(): %v\n", err)
		return reconcileResult, err
	}

	return reconcileResult, nil
}
