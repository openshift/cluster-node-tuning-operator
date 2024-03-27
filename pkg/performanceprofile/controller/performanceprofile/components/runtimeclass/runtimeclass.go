package runtimeclass

import (
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// New returns a new RuntimeClass object
func New(name string, profile *performancev2.PerformanceProfile, handler string) *nodev1.RuntimeClass {
	return &nodev1.RuntimeClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RuntimeClass",
			APIVersion: "node.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Handler: handler,
		Scheduling: &nodev1.Scheduling{
			NodeSelector: profile.Spec.NodeSelector,
		},
	}
}

func BuildRuntimeClassName(performanceProfileName string) string {
	return components.GetComponentName(performanceProfileName, components.ComponentNamePrefix)
}
