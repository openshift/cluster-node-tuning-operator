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
	//	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	resyncPeriodDefault int64 = 60
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

	//	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForObject{})
	//	if err != nil {
	//		return err
	//	}

	//	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	//	// Watch for changes to secondary resource Pods and requeue the owner Tuned
	//	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
	//		IsController: true,
	//		OwnerType:    &tunedv1alpha1.Tuned{},
	//	})
	//	if err != nil {
	//		return err
	//	}

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

func syncNamespace(r *ReconcileTuned) error {
	log.Printf("syncNamespace()")
	ns, err := r.manifestFactory.TunedNamespace()
	if err != nil {
		return fmt.Errorf("couldn't build tuned namespace: %v", err)
	}
	err = r.client.Create(context.TODO(), ns)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned namespace: %v", err)
		} else {
			// tuned namespace already exists
			log.Printf("tuned namespace already exists, updating")
			err = r.client.Update(context.TODO(), ns)
			if err != nil {
				return fmt.Errorf("couldn't update tuned namespace: %v", err)
			}
		}
	}
	return nil
}

func syncServiceAccount(r *ReconcileTuned) error {
	log.Printf("syncServiceAccount()")
	sa, err := r.manifestFactory.TunedServiceAccount()
	if err != nil {
		return fmt.Errorf("couldn't build tuned service account: %v", err)
	}
	err = r.client.Create(context.TODO(), sa)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned service account: %v", err)
		} else {
			// tuned service account already exists
			log.Printf("tuned service account already exists, updating")
			err = r.client.Update(context.TODO(), sa)
			if err != nil {
				return fmt.Errorf("couldn't update tuned service account: %v", err)
			}
		}
	}
	return nil
}

func syncClusterRole(r *ReconcileTuned) error {
	log.Printf("syncClusterRole()")
	cr, err := r.manifestFactory.TunedClusterRole()
	if err != nil {
		return fmt.Errorf("couldn't build tuned cluster role: %v", err)
	}
	err = r.client.Create(context.TODO(), cr)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned cluster role: %v", err)
		} else {
			// tuned cluster role already exists
			//			log.Printf("tuned cluster role already exists, updating")
			//			err = r.client.Update(context.TODO(), cr)
			//			if err != nil {
			//				return fmt.Errorf("couldn't update tuned cluster role: %v", err)
			//			}
		}
	}
	return nil
}

func syncClusterRoleBinding(r *ReconcileTuned) error {
	log.Printf("syncClusterRoleBinding()")
	crb, err := r.manifestFactory.TunedClusterRoleBinding()
	if err != nil {
		return fmt.Errorf("couldn't build tuned cluster role binding: %v", err)
	}
	err = r.client.Create(context.TODO(), crb)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned cluster role binding: %v", err)
		} else {
			//			// tuned cluster role binding already exists
			//			log.Printf("tuned cluster role binding already exists, updating")
			//			err = r.client.Update(context.TODO(), crb)
			//			if err != nil {
			//				return fmt.Errorf("couldn't update tuned cluster role binding: %v", err)
			//			}
		}
	}
	return nil
}

func syncClusterConfigMapProfiles(r *ReconcileTuned, tunedInstance *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterConfigMapProfiles()")
	cmProfiles, err := r.manifestFactory.TunedConfigMapProfiles(tunedInstance)
	if err != nil {
		return fmt.Errorf("couldn't build tuned profiles config map: %v", err)
	}
	err = r.client.Create(context.TODO(), cmProfiles)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("couldn't create tuned profiles config map: %v", err)
	}
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned profiles config map: %v", err)
		} else {
			// tuned profiles config map already exists
			log.Printf("tuned profiles config map already exists, updating")
			err = r.client.Update(context.TODO(), cmProfiles)
			if err != nil {
				return fmt.Errorf("couldn't update tuned profiles config map: %v", err)
			}
		}
	}
	return nil
}

func syncClusterConfigMapRecommend(r *ReconcileTuned, tunedInstance *tunedv1alpha1.Tuned) error {
	log.Printf("syncClusterConfigMapRecommend()")
	cmRecommend, err := r.manifestFactory.TunedConfigMapRecommend(tunedInstance)
	if err != nil {
		return fmt.Errorf("couldn't build tuned recommend config map: %v", err)
	}
	err = r.client.Create(context.TODO(), cmRecommend)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned recommend config map: %v", err)
		} else {
			// tuned recommend config map already exists
			log.Printf("tuned recommend config map already exists, updating")
			err = r.client.Update(context.TODO(), cmRecommend)
			if err != nil {
				return fmt.Errorf("couldn't update tuned recommend config map: %v", err)
			}
		}
	}
	return nil
}

func syncDaemonSet(r *ReconcileTuned) error {
	log.Printf("syncDaemonSet()")
	ds, err := r.manifestFactory.TunedDaemonSet()
	if err != nil {
		return fmt.Errorf("couldn't build tuned daemonset: %v", err)
	}
	err = r.client.Create(context.TODO(), ds)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create tuned daemonset: %v", err)
		} else {
			// tuned daemonset already exists
			log.Printf("tuned daemonset already exists, updating")
			err = r.client.Update(context.TODO(), ds)
			if err != nil {
				return fmt.Errorf("couldn't update tuned daemonset: %v", err)
			}
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
	var resyncPeriodDuration int64 = resyncPeriodDefault

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

	err = syncNamespace(r)
	if err != nil {
		return reconcileResult, err
	}

	err = syncServiceAccount(r)
	if err != nil {
		return reconcileResult, err
	}

	err = syncClusterRole(r)
	if err != nil {
		return reconcileResult, err
	}

	err = syncClusterRoleBinding(r)
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

	err = syncDaemonSet(r)
	if err != nil {
		return reconcileResult, err
	}

	return reconcileResult, nil
}
