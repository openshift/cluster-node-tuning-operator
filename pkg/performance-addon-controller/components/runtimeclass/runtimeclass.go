package runtimeclass

import (
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/components"

	nodev1beta1 "k8s.io/api/node/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// New returns a new RuntimeClass object
func New(profile *performancev2.PerformanceProfile, handler string) *nodev1beta1.RuntimeClass {
	name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	return &nodev1beta1.RuntimeClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RuntimeClass",
			APIVersion: "node.k8s.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Handler: handler,
		Scheduling: &nodev1beta1.Scheduling{
			NodeSelector: profile.Spec.NodeSelector,
		},
	}
}
