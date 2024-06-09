package client

import (
	"context"
	"time"

	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"

	apiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	configv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	performancev1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	performancev1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1alpha1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	hypershiftutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

var (
	// Client kept for backport compatibility until all tests will be converted
	Client client.Client
	// ControlPlaneClient defines the API client to run CRUD operations on the control plane cluster,
	//that will be used for testing
	ControlPlaneClient client.Client
	// DataPlaneClient defines the API client to run CRUD operations on the data plane cluster,
	//that will be used for testing
	DataPlaneClient client.Client
	// K8sClient defines k8s client to run subresource operations, for example you should use it to get pod logs
	K8sClient *kubernetes.Clientset
	// ClientsEnabled tells if the client from the package can be used
	ClientsEnabled bool
)

func init() {
	// Setup Scheme for all resources
	if err := performancev2.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := performancev1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := performancev1alpha1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := configv1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := mcov1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := tunedv1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := apiextensionsv1beta1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := hypershiftv1beta1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	if err := apiv1beta1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	var err error
	Client, err = newClient()
	if err != nil {
		testlog.Info("Failed to initialize client, check the KUBECONFIG env variable", err.Error())
		return
	}

	ControlPlaneClient, err = NewControlPlane()
	if err != nil {
		testlog.Info("Failed to initialize ControlPlaneClient client, check the KUBECONFIG env variable", err.Error())
		return
	}
	DataPlaneClient, err = NewDataPlane()
	if err != nil {
		testlog.Info("Failed to initialize DataPlaneClient client, check the KUBECONFIG env variable", err.Error())
		return
	}
	K8sClient, err = NewK8s()
	if err != nil {
		testlog.Info("Failed to initialize k8s client, check the KUBECONFIG env variable", err.Error())
		return
	}
	ClientsEnabled = true
}

func newClient() (client.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}
	return client.New(cfg, client.Options{})
}

// NewControlPlane returns a new controller-runtime client for ControlPlaneClient cluster.
func NewControlPlane() (client.Client, error) {
	if hypershiftutils.IsHypershiftCluster() {
		testlog.Info("creating ControlPlaneClient client for hypershift cluster")
		return hypershiftutils.BuildControlPlaneClient()
	}
	return newClient()
}

// NewDataPlane returns a new controller-runtime client for DataPlaneClient cluster.
func NewDataPlane() (client.Client, error) {
	var c client.Client
	var err error
	if hypershiftutils.IsHypershiftCluster() {
		testlog.Info("creating DataPlaneClient client for hypershift cluster")
		c, err = hypershiftutils.BuildDataPlaneClient()
	} else {
		c, err = newClient()
	}
	return &dataPlaneImpl{c}, err
}

// NewK8s returns a kubernetes clientset
func NewK8s() (*kubernetes.Clientset, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Exit(err.Error())
	}
	return clientset, nil
}

func GetWithRetry(ctx context.Context, cli client.Client, key client.ObjectKey, obj client.Object) error {
	var err error
	EventuallyWithOffset(1, func() error {
		err = cli.Get(ctx, key, obj)
		if err != nil {
			testlog.Infof("Getting %s failed, retrying: %v", key.Name, err)
		}
		return err
	}, 1*time.Minute, 10*time.Second).ShouldNot(HaveOccurred(), "Max numbers of retries getting %v reached", key)
	return err
}
