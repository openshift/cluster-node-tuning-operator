package framework

import (
	clientconfigv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	tunedv1client "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned/typed/tuned/v1"
	clientmachineconfigv1 "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned/typed/machineconfiguration.openshift.io/v1"
)

type ClientSet struct {
	corev1client.CoreV1Interface
	appsv1client.AppsV1Interface
	clientconfigv1.ConfigV1Interface
	tunedv1client.TunedV1Interface
	clientmachineconfigv1.MachineconfigurationV1Interface
}

// NewClientSet returns a *ClientBuilder with the given kubeconfig.
func NewClientSet() *ClientSet {
	kubeconfig, err := ntoclient.GetConfig()
	if err != nil {
		panic(err)
	}

	clientSet := &ClientSet{}
	clientSet.CoreV1Interface = corev1client.NewForConfigOrDie(kubeconfig)
	clientSet.ConfigV1Interface = clientconfigv1.NewForConfigOrDie(kubeconfig)
	clientSet.TunedV1Interface = tunedv1client.NewForConfigOrDie(kubeconfig)
	clientSet.AppsV1Interface = appsv1client.NewForConfigOrDie(kubeconfig)
	clientSet.MachineconfigurationV1Interface = clientmachineconfigv1.NewForConfigOrDie(kubeconfig)

	return clientSet
}
