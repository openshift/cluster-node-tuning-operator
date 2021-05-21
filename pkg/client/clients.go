package client

import (
	kubeset "k8s.io/client-go/kubernetes"
	appsset "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreset "k8s.io/client-go/kubernetes/typed/core/v1"

	configclientset "github.com/openshift/client-go/config/clientset/versioned"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	tunedset "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned"
	mcfgclientset "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned"
)

type Clients struct {
	Kube            *kubeset.Clientset
	ConfigClientSet *configclientset.Clientset
	ConfigV1Client  *configv1client.ConfigV1Client
	Tuned           *tunedset.Clientset
	MC              *mcfgclientset.Clientset
	Core            *coreset.CoreV1Client
	Apps            *appsset.AppsV1Client
}
