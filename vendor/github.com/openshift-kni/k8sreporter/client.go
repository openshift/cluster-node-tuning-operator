package k8sreporter

import (
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type clientSet struct {
	corev1client.CoreV1Interface
	appsv1client.AppsV1Interface
	runtimeclient.Client
}

// New returns a *ClientBuilder with the given kubeconfig.
func newClient(kubeconfig string, crScheme *runtime.Scheme) (*clientSet, error) {
	var config *rest.Config
	var err error

	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	if kubeconfig != "" {
		fmt.Printf("Loading kube client config from path %q\n", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		fmt.Println("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to init client, err: %w", err)
	}

	clientSet := &clientSet{}
	clientSet.CoreV1Interface = corev1client.NewForConfigOrDie(config)
	clientSet.AppsV1Interface = appsv1client.NewForConfigOrDie(config)

	clientSet.Client, err = runtimeclient.New(config, client.Options{
		Scheme: crScheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init runtimeclient, err: %w", err)
	}
	return clientSet, nil
}
