package client

import (
	kappslisters "k8s.io/client-go/listers/apps/v1"
	kcorelisters "k8s.io/client-go/listers/core/v1"

	configlisters "github.com/openshift/client-go/config/listers/config/v1"

	ntolisters "github.com/openshift/cluster-node-tuning-operator/pkg/generated/listers/tuned/v1"
	mcfglisters "github.com/openshift/machine-config-operator/pkg/generated/listers/machineconfiguration.openshift.io/v1"
)

type Listers struct {
	DaemonSets         kappslisters.DaemonSetNamespaceLister
	Pods               kcorelisters.PodLister
	Nodes              kcorelisters.NodeLister
	ClusterOperators   configlisters.ClusterOperatorLister
	TunedResources     ntolisters.TunedNamespaceLister
	TunedProfiles      ntolisters.ProfileNamespaceLister
	MachineConfigs     mcfglisters.MachineConfigLister
	MachineConfigPools mcfglisters.MachineConfigPoolLister
}
