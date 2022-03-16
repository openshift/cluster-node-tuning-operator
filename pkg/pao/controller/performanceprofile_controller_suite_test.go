package controller

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/pao/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	olmv1 "github.com/operator-framework/api/pkg/operators/v1"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"

	"k8s.io/client-go/kubernetes/scheme"
)

func TestPerformanceProfile(t *testing.T) {
	RegisterFailHandler(Fail)

	// add resources API to default scheme
	performancev2.AddToScheme(scheme.Scheme)
	configv1.AddToScheme(scheme.Scheme)
	mcov1.AddToScheme(scheme.Scheme)
	tunedv1.AddToScheme(scheme.Scheme)
	olmv1.AddToScheme(scheme.Scheme)
	olmv1alpha1.AddToScheme(scheme.Scheme)

	RunSpecs(t, "Performance Profile Suite")
}
