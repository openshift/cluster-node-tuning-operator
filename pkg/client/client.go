package client

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// GetConfig creates a *rest.Config for talking to a Kubernetes apiserver.
// Otherwise will assume running in cluster and use the cluster provided kubeconfig.
//
// Config precedence
//
// * KUBECONFIG environment variable pointing at a file
//
// * In-cluster config if running in cluster
//
// * $HOME/.kube/config if exists
func GetConfig() (*rest.Config, error) {
	// If an env variable is specified with the config locaiton, use that
	if len(os.Getenv("KUBECONFIG")) > 0 {
		return clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	}
	// If no explicit location, try the in-cluster config
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	// If no in-cluster config, try the default location in the user's home directory
	if usr, err := user.Current(); err == nil {
		if c, err := clientcmd.BuildConfigFromFlags(
			"", filepath.Join(usr.HomeDir, ".kube", "config")); err == nil {
			return c, nil
		}
	}

	return nil, fmt.Errorf("could not locate a kubeconfig")
}

// GetCfgV1Client returns OpenShift *v1.ConfigV1Client for talking to a Kubernetes apiserver.
func GetCfgV1Client() (*configv1client.ConfigV1Client, error) {
	c, err := GetConfig()
	if err != nil {
		return nil, err
	}

	operatorClient, err := configv1client.NewForConfig(c)
	if err != nil {
		return nil, err
	}

	return operatorClient, nil
}
