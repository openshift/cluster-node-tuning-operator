package client

import (
	kappslisters "k8s.io/client-go/listers/apps/v1"
	kcorelisters "k8s.io/client-go/listers/core/v1"
	krbaclisters "k8s.io/client-go/listers/rbac/v1"

	configlisters "github.com/openshift/client-go/config/listers/config/v1"

	ntolisters "github.com/openshift/cluster-node-tuning-operator/pkg/generated/listers/tuned/v1"
)

type Listers struct {
	DaemonSets          kappslisters.DaemonSetNamespaceLister
	Pods                kcorelisters.PodLister
	Nodes               kcorelisters.NodeLister
	Secrets             kcorelisters.SecretNamespaceLister
	ServiceAccounts     kcorelisters.ServiceAccountNamespaceLister
	ClusterRoles        krbaclisters.ClusterRoleLister
	ClusterRoleBindings krbaclisters.ClusterRoleBindingLister
	ClusterOperators    configlisters.ClusterOperatorLister
	TunedResources      ntolisters.TunedNamespaceLister
	TunedProfiles       ntolisters.ProfileNamespaceLister
}
