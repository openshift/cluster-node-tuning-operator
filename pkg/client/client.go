package client

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	operatorv1 "github.com/openshift/api/operator/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"

	"k8s.io/apimachinery/pkg/runtime"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

var (
	// scheme contains all the API types necessary for the operator's dynamic
	// clients to work. Any new non-core types must be added here.
	//
	// NOTE: The discovery mechanism used by the client won't automatically refresh,
	// so only add types here that are _guaranteed_ to exist before the operator
	// starts.
	scheme *runtime.Scheme
)

func init() {
	scheme = kscheme.Scheme
	if err := operatorv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := tunedv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
}

// GetConfig creates a *rest.Config for talking to a Kubernetes apiserver.
//
// Config precedence
//
// * KUBECONFIG environment variable pointing at a file
// * In-cluster config if running in cluster
// * $HOME/.kube/config if exists
func GetConfig() (*rest.Config, error) {
	configFromFlags := func(kubeConfig string) (*rest.Config, error) {
		if _, err := os.Stat(kubeConfig); err != nil {
			return nil, fmt.Errorf("Cannot stat kubeconfig '%s'", kubeConfig)
		}
		return clientcmd.BuildConfigFromFlags("", kubeConfig)
	}

	// If an env variable is specified with the config location, use that
	kubeConfig := os.Getenv("KUBECONFIG")
	if len(kubeConfig) > 0 {
		return configFromFlags(kubeConfig)
	}
	// If no explicit location, try the in-cluster config
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	// If no in-cluster config, try the default location in the user's home directory
	if usr, err := user.Current(); err == nil {
		kubeConfig := filepath.Join(usr.HomeDir, ".kube", "config")
		return configFromFlags(kubeConfig)
	}

	return nil, fmt.Errorf("Could not locate a kubeconfig")
}

func GetScheme() *runtime.Scheme {
	return scheme
}

// NewClient builds an operator-compatible kube client from the given REST config.
func NewClient(kubeConfig *rest.Config) (client.Client, error) {
	mapper, err := apiutil.NewDiscoveryRESTMapper(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to discover api rest mapper: %v", err)
	}
	kubeClient, err := client.New(kubeConfig, client.Options{
		Scheme: scheme,
		Mapper: mapper,
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to create kube client: %v", err)
	}
	return kubeClient, nil
}

func GetClient() client.Client {
	// Get a kube client.
	kubeConfig, err := GetConfig()
	if err != nil {
		return nil
	}
	kubeClient, err := NewClient(kubeConfig)
	if err != nil {
		return nil
	}
	return kubeClient
}
