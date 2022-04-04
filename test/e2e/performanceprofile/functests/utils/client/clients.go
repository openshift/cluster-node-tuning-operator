package client

import (
	"context"
	"time"

	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	configv1 "github.com/openshift/api/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	performancev1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	performancev1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1alpha1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
)

var (
	// Client defines the API client to run CRUD operations, that will be used for testing
	Client client.Client
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

	if err := operatorsv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		klog.Exit(err.Error())
	}

	var err error
	Client, err = New()
	if err != nil {
		testlog.Info("Failed to initialize client, check the KUBECONFIG env variable", err.Error())
		ClientsEnabled = false
		return
	}
	K8sClient, err = NewK8s()
	if err != nil {
		testlog.Info("Failed to initialize k8s client, check the KUBECONFIG env variable", err.Error())
		ClientsEnabled = false
		return
	}
	ClientsEnabled = true
}

// New returns a controller-runtime client.
func New() (client.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	c, err := client.New(cfg, client.Options{})
	return c, err
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

func GetWithRetry(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	var err error
	EventuallyWithOffset(1, func() error {
		err = Client.Get(ctx, key, obj)
		if err != nil {
			testlog.Infof("Getting %s failed, retrying: %v", key.Name, err)
		}
		return err
	}, 1*time.Minute, 10*time.Second).ShouldNot(HaveOccurred(), "Max numbers of retries getting %v reached", key)
	return err
}
